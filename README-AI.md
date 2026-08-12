# Playing agario with an AI agent

agario ships as a [Gymnasium](https://gymnasium.farama.org/) environment, so a
reinforcement-learning agent can play the same game a human plays, against the same
bots, in the same simulation.

This is the tutorial. [`python/README.md`](python/README.md) is the reference.

## 1. Install it

You need **Python 3.10 or newer** and **[Go](https://go.dev/dl/)**.

From the top of this repository:

```sh
go build -o agario-env ./cmd/agario-env
pip install -e python
```

The first command builds a small program that serves the game. The second installs the
Python package that talks to it. (If you skip the first, the package will try to build it
for you the first time you use it, and cache the result.)

Check it works:

```sh
python -c "
import gymnasium, agario_gym
env = gymnasium.make('Agario-Small-v0', render_mode='ansi')
obs, info = env.reset(seed=0)
print(env.render())
for _ in range(100):
    obs, reward, terminated, truncated, info = env.step(env.action_space.sample())
print(env.render())
env.close()"
```

## 2. What the agent controls

One cell, in a 4000×4000 arena, against the game's own AI bots. An action is two numbers:

```python
action = [heading, trigger]
```

- **heading**, `0..15`, is a compass direction: `angle = heading · 2π/16`. The cell steers
  that way for the whole step.
- **trigger** is `0` do nothing, `1` split, `2` eject mass.

Splitting or ejecting below the mass threshold is a no-op, not an error.
`info["action_mask"]` tells you which triggers are legal right now.

One step is four simulation ticks — 1/30 s of game time — so the agent makes 30 decisions
per second of play, not 120.

## 3. What the agent sees

Two encodings, chosen with `obs_mode`:

```python
gymnasium.make("Agario-Small-v0", obs_mode="vector")   # default, for an MLP
gymnasium.make("Agario-Small-v0", obs_mode="raster")   # for a CNN
```

`vector` is a flat array: the agent's own mass and cells, the four wall distances, its
rank, and then the nearest food, enemy cells, viruses and ejected pellets as offsets
relative to its own centre of mass.

The important detail is that **positions are divided by the agent's view radius**, which
grows with its mass. A tiny cell and a huge one therefore see their surroundings on the
same numeric scale, which is what lets a single policy handle the whole game rather than
only the size it was trained at. Masses go through `log1p` for the same reason: mass spans
three orders of magnitude.

Enemy cells arrive pre-tagged as threat or prey, using the game's own eating rule, so the
agent never has to rediscover the 1.25 mass ratio from raw numbers.

The agent only sees what is within its view radius — the same horizon the camera gives a
human player.

## 4. Reward

The default pays the **log ratio of mass** across the step, plus a bonus per cell eaten
and a penalty for dying:

```python
gymnasium.make("Agario-Small-v0", reward_scheme="mass_delta")  # the default
```

The log matters. With a plain mass difference, growing from 20 to 40 is worth 20 and
growing from 2000 to 2040 is also worth 20 — so the opening of an episode, where all the
learning has to happen, is worth nothing next to the endgame. A log ratio makes doubling
worth the same at every scale.

`reward_scheme="sparse"` pays only the death penalty; `"survival"` pays a flat rate for
staying alive. `kill_bonus`, `death_penalty` and `step_cost` are constructor arguments.

## 5. Know what "good" looks like before you train

```sh
python python/examples/random_agent.py --episodes 5
```

```
policy    final mass  best mass  survived    reward
---------------------------------------------------
random           7.2       28.2     23.0s     -3.63
greedy          72.8       72.8     30.0s      1.07
```

`greedy` is twenty lines of heuristic: run from anything that can eat you, otherwise eat
the nearest thing you can. It never splits. **A learned policy that does not beat it has
not learned anything worth having**, whatever its reward curve looked like.

## 6. Train something

```sh
pip install -e "python[rl]"
python python/examples/train_ppo.py --steps 200000
```

The example uses the batched environment, so all the parallel worlds live in one server
process:

```python
envs = gymnasium.make_vec("Agario-Small-v0", num_envs=16)
```

That is much cheaper than `SubprocVecEnv`: no extra Python interpreters, and one round
trip per batched step instead of sixteen.

Start on `Agario-Small-v0` (4 bots, 200 food). `Agario-v0` is the full game and is a much
harder exploration problem.

## 7. What to expect

Four 1M-step PPO runs (`MlpPolicy`, 16 batched worlds, `n_steps=256`, `lr=3e-4`), each
evaluated over 5 episodes against the same baselines. Best mass reached:

| setting | random | greedy | PPO |
|---|---|---|---|
| `Agario-Small-v0` (200 food, 4 bots, 4 viruses) | 28 | **76** | 29 |
| the same, `death_penalty=0.5` | 28 | **76** | 31 |
| 2000 food, **no bots or viruses** | 34 | 139 | **338** |
| 1000 food, 4 bots, 4 viruses | 30 | **299** | 189 |

Read those rows together, because separately each one misleads.

**PPO learns the game readily when nothing is hunting it** — row three, where it beats the
heuristic by 2.4×. So the observation encoding, the action mapping and the reward are not
the obstacle; the pipeline works.

**It is beaten whenever opponents are present**, even with food five times denser than the
default (row four, where it reaches 63% of greedy while still surviving the full episode).
On the shipped `Agario-Small-v0` it barely grows at all.

Two things this cost me, so you do not repeat them:

- **The death penalty is not the problem.** It looks like it should be: one death costs 5
  while tripling your mass over a whole episode earns only `log(3) ≈ 1.1`, so the
  arithmetic says the agent should hide. Dropping the penalty tenfold moved best mass from
  29 to 31. The hypothesis was clean, plausible, and wrong.
- **Inspect the policy before theorising about the reward.** One diagnostic settled it: the
  trained policy agreed with the greedy heading 4% of the time, against 6% for random
  guessing, and spent 60% of its steps requesting a split. Splitting is a no-op below 36
  mass, so nothing in the gradient discourages it — the policy had not learned to steer at
  all, and no amount of reward rebalancing was going to change that.

The gap is exploration, not machinery. Food at the default density is rare enough that a
policy which has not yet learned to steer almost never eats, so the signal that would
teach it to steer never arrives; the bots then end the episode before it can. Starting on
dense food and annealing towards the real density is the obvious next thing to try.

## 8. Reproducibility

```python
obs, info = env.reset(seed=1234)
```

seeds Gymnasium's generator, which draws the concrete world seed sent to the server. The
same seed gives the same episode, and `python/tests/test_seeding.py` asserts it.

Worth knowing: unlike a puzzle game, the world here is *generated* randomly, and that
generation happens in Go. The seed therefore has to cross the wire. `options={"seed": n}`
pins the world directly, independently of the Gymnasium seed.

## 9. Talking to the server yourself

You do not need Python. The protocol is one JSON object per line, and it is meant to be
typed by hand:

```console
$ ./agario-env
{"id":1,"cmd":"hello"}
{"id":2,"cmd":"reset","env_id":0,"seed":7}
{"id":3,"cmd":"step","env_id":0,"action":[4,1]}
```

Commands are `hello`, `reset`, `step`, `state`, `reset_batch`, `step_batch` and `close`.
The server exits when its stdin closes.

The design rule is that **the server reports facts and never a reward**. The reward, the
observation tensor and the episode budget all live in the Python client, so you can change
any of them without touching Go. The handshake reports the arena size, the frame skip and
every array length, so the client never hardcodes a shape.

## 10. Troubleshooting

| Symptom | Cause |
|---|---|
| `BinaryNotFound` | No `agario-env` built and no Go toolchain. Build it, or set `$AGARIO_ENV_BIN`. |
| `ModuleNotFoundError: agario_gym` | `pip install -e python` not run, or a different interpreter. |
| `EngineDied` | The server crashed or was killed. Its stderr goes to your terminal. |
| `CommandFailed: not_reset` | `step()` before `reset()`. |
| `CommandFailed: bad_action` | A heading outside `0..15` or a trigger outside `0..2`. |
| Observation shape mismatch | The server's `-k-*` flags changed without rebuilding the env; the spaces come from the handshake, so construct a fresh env. |
| Episodes never end | Constructing `AgarioEnv` directly gives no step budget. Use `gymnasium.make`, or pass `max_episode_steps` to the vector env. |
