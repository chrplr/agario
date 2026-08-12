# agario-gym

[agario](https://github.com/chrplr/agario) as a [Gymnasium](https://gymnasium.farama.org/)
environment. The agent plays one cell against the game's own AI bots, in the same
simulation the playable game runs.

```python
import gymnasium, agario_gym

env = gymnasium.make("Agario-Small-v0")
obs, info = env.reset(seed=0)
obs, reward, terminated, truncated, info = env.step(env.action_space.sample())
env.close()
```

## Install

Needs Python 3.10+ and, unless you already have the binary, [Go](https://go.dev/dl/).
From the top of the repository:

```sh
go build -o agario-env ./cmd/agario-env
pip install -e python
```

The first command builds the server. The second installs this package, which talks to it.
If you skip the first, the package builds it for you on first use and caches the result.
`$AGARIO_ENV_BIN` overrides the search.

## Environment ids

| id | opponents | budget |
|---|---|---|
| `Agario-v0` | 15 bots, 800 food, 25 viruses | 3000 steps |
| `Agario-Small-v0` | 4 bots, 200 food, 4 viruses | 1500 steps |
| `Agario-NoVirus-v0` | 15 bots, 800 food, no viruses | 3000 steps |

agario has no natural end — an agent that keeps surviving keeps playing — so every id
carries a `TimeLimit`. At the default frame skip one step is 1/30 s of game time, so 3000
steps is about a hundred seconds of play.

Constructor keywords (`n_food`, `n_bots`, `n_viruses`, `frames`, `k_food`, `view_scale`, …)
override any of it; `gymnasium.make("Agario-v0", n_bots=2)` works.

## Action space

`MultiDiscrete([16, 3])` — `[heading, trigger]`.

- **heading** `0..15` is a compass direction, `angle = heading · 2π/16`. The cell steers
  that way for the whole step. Sixteen is ample: a cell turns slowly enough that finer
  aiming is wasted.
- **trigger** `0` nothing, `1` split, `2` eject mass. Both are no-ops when the cell is
  below the mass threshold, which is not an error; `info["action_mask"]` says which are
  legal right now, and `env.action_masks()` returns the same array for a masking policy.

## Observation space

Two encodings, chosen with `obs_mode`:

- **`vector`** (default) — `Box(-1, 1, (n,), float32)`, a flat egocentric feature vector:
  own mass, cell count, trigger legality, the four wall distances, rank and mass share,
  then the nearest cells, food, viruses and ejected pellets as `dx, dy, mass` relative to
  the agent's centre of mass. Positions are divided by the agent's view radius, so a small
  cell and a huge one see their surroundings on the same numeric scale — which is what
  lets one policy handle both. Masses go through `log1p`, because mass spans three orders
  of magnitude.
- **`raster`** — `Box(0, 1, (5, 48, 48), float32)`, a local view centred on the agent with
  one channel each for food, own cells, cells that can eat it, cells it can eat, and
  viruses. For convolutional policies.

Both are built here in numpy from the wire state, so trying a third costs an edit rather
than a rebuild of the Go binary.

## Reward

`reward_scheme` selects one of:

- **`mass_delta`** (default) — `log(mass_after / mass_before)`, plus `kill_bonus` per cell
  eaten and `-death_penalty` on dying. The log ratio matters: mass spans three orders of
  magnitude, so a plain difference makes the opening of an episode worth nothing beside
  the endgame and the agent never learns to start.
- **`sparse`** — only the death penalty.
- **`survival`** — a flat `step_cost` per surviving step, plus the death penalty.

`kill_bonus`, `death_penalty` and `step_cost` are constructor keywords.

`info["kills"]` is an approximation, not a fact: the simulation records no kill count, so
the server infers one from cells that vanished from view while the agent gained mass. Use
it for a bonus term, not for scoring.

## Vector environment

```python
envs = gymnasium.make_vec("Agario-Small-v0", num_envs=16)
```

N worlds share one server process, addressed by `env_id`, with one round trip per batched
step. Autoreset follows the `NEXT_STEP` convention. The test suite asserts it matches
`SyncVectorEnv` step for step at the same seed.

## Seeding

`reset(seed=…)` seeds `self.np_random`, which draws the concrete world seed sent to the
server. Reproducibility belongs to Python, but unlike a puzzle game the world itself is
generated in Go, so the seed has to cross the wire explicitly. `options={"seed": n}` pins
it directly.

## The wire protocol

One JSON object per line in each direction, request and response paired by `id`. It is
meant to be readable, and driveable by hand:

```console
$ ./agario-env
{"id":1,"cmd":"hello"}
{"id":2,"cmd":"reset","env_id":0,"seed":7}
{"id":3,"cmd":"step","env_id":0,"action":[4,1]}
```

Commands: `hello`, `reset`, `step`, `state`, `reset_batch`, `step_batch`, `close`. The
server reports facts and never a reward — the reward, the termination rule and the
observation tensor all live here, so changing any of them never rebuilds Go.

Spaces come from the `hello` handshake rather than constants duplicated on both sides.
Every neighbour array is padded to a handshake-declared length, so the observation shape
never depends on how many entities happen to be nearby.

The server culls entities to the agent's view radius before sending them. That radius is
what the camera shows a human player, so the agent gets no information the game withholds.

## Performance

Roughly **3,000 agent steps/second** with 16 batched worlds at full population (800 food,
15 bots, 25 viruses), on an Intel Core Ultra 7 165H — about 1,400/s for a single world,
2,900 at 16, 3,400 at 32, median of three runs each. A 200k-step training run is therefore
a minute or so of environment time.

The simulation itself is not the constraint: it steps at ~40,000 ticks/second in Go. The
cost is the JSON round trip and rebuilding the observation in numpy. Measured on this
machine: the wire alone sustains ~4,750 steps/s at 16 worlds, and encoding one `vector`
observation takes ~75 µs.

If you need more, `k_food` is the knob — it dominates both the payload and the encoding.
Dropping `k_food=32, k_cells=16` to `8, 4` measured ~40% faster, at the cost of a shorter
horizon for the agent.

## Tests

```sh
pip install -e "python[dev]"
pytest python/tests -q
```

Includes `gymnasium.utils.env_checker.check_env` for both observation modes with warnings
escalated to errors.
