# agario

[![CI](https://github.com/chrplr/agario/actions/workflows/ci.yml/badge.svg)](https://github.com/chrplr/agario/actions/workflows/ci.yml)

A single-player [agar.io](https://en.wikipedia.org/wiki/Agar.io) clone in Go, rendered with
SDL3 via [`Zyko0/go-sdl3`](https://github.com/Zyko0/go-sdl3). You play one cell against
AI bots in a 4000×4000 arena, with food, splitting, mass ejection and viruses.

**▶ Play it in your browser: <https://chrplr.github.io/agario/>**

Implements the specification in [`description.md`](description.md).

An AI agent can play it too: the simulation is served as a
[Gymnasium](https://gymnasium.farama.org/) environment — see
[Letting an agent play](#letting-an-agent-play).

## Install

### Download a binary

Grab the archive for your platform from the
[releases page](https://github.com/chrplr/agario/releases), unpack it and run `agario`.
Builds are published for Linux, macOS and Windows on both amd64 and arm64.

**No SDL3 installation is required.** The binary uses the system SDL3 if one is present
and otherwise unpacks its own bundled copy to a temporary directory. There is no cgo, so
nothing needs a compiler or extra runtime libraries.

```bash
tar xzf agario-v0.1.0-linux-amd64.tar.gz
./agario
```

**macOS.** The binaries are unsigned and unnotarized, so Gatekeeper quarantines anything
downloaded through a browser and refuses to launch it ("cannot be opened because the
developer cannot be verified"). Strip the quarantine attribute:

```bash
xattr -d com.apple.quarantine agario
./agario
```

If `xattr` reports `No such xattr`, the file was never quarantined — downloading with
`curl` or `wget` does not set it — and you can just run it. The alternative is to try
launching once, then approve it under **System Settings → Privacy & Security**.

**Windows.** SmartScreen may warn about an unrecognized publisher for the same reason;
choose *More info → Run anyway*. A console window opens alongside the game, which is
deliberate: the CLI modes below print there.

### Build from source

Requires **Go 1.25+**. Nothing else — no C compiler, no SDL development headers.

```bash
git clone https://github.com/chrplr/agario
cd agario
go build          # produces ./agario
./agario
```

Or run it straight from the checkout with `go run .`.

A `Makefile` wraps the commands on this page; `make help` lists them. The ones worth
knowing are `make build` (game and environment server, version stamped from
`git describe`), `make ci` (what [`ci.yml`](.github/workflows/ci.yml) runs, so a
failure shows up before the push), `make cross` (all six release targets into `dist/`)
and `make wasm` (the browser bundle — see below).

To cross-compile, set `GOOS`/`GOARCH`. Because `go-sdl3` is a purego binding that opens
SDL3 at runtime rather than linking it, every supported target builds from any host with
no SDK or cross-toolchain:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -o agario.exe .
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -o agario-macos .
```

Supported targets are Linux, macOS and Windows on amd64 and arm64 — all
[purego Tier 1](https://github.com/ebitengine/purego#supported-platforms), all cgo-free.
Stamp a version into the binary with
`-ldflags="-X main.version=$(git describe --tags)"`; `agario -version` prints it.

On Linux you can shrink the binary by ~1.4 MB by dropping the embedded SDL fallback, at
the cost of requiring `libsdl3-0` on the target machine — see `loadSDL` in `main.go`.

### Build for the browser (WebAssembly)

The browser build needs [`chrplr/go-sdl3-wasm`](https://github.com/chrplr/go-sdl3-wasm)
(branch `wasm-render-fixes`), cloned as a **sibling directory**. Upstream `go-sdl3`
compiles for `GOOS=js` but is not usable: it ships most js bindings stubbed with
`panic("not implemented on js")` — including `SetTextureColorMod`, `SetAlphaMod`,
`SetBlendMode` and `SetScaleMode`, which every cell here depends on — and it never sizes
the canvas, so nothing renders at all.

```bash
git clone https://github.com/chrplr/go-sdl3-wasm ../go-sdl3-wasm
cd ../go-sdl3-wasm && git checkout wasm-render-fixes && cd -

go mod edit -replace github.com/Zyko0/go-sdl3=../go-sdl3-wasm
go run ../go-sdl3-wasm/cmd/wasmsdl serve -html web/index.html .   # localhost:8080
git checkout go.mod                                               # see below
```

`make wasm-serve` does the same three steps, and `make wasm` writes the bundle to
`dist/` instead of serving it. Both restore `go.mod` afterwards even if the build fails
or you interrupt it; pass `WASM_FORK=/path/to/checkout` if the fork is not a sibling.

**Never commit that `replace`.** `go.mod` stays on the published `go-sdl3` so `go get`,
the CI job and the six-platform release build keep working for everyone; the fork is
applied only for a browser build, and in CI only ([`.github/workflows/pages.yml`](.github/workflows/pages.yml)).

`wasmsdl` emits five files — `index.html`, `wasm_exec.js`, `main.wasm`, `sdl.js`,
`sdl.wasm` — about 6 MB total, roughly 2.2 MB gzipped. `sdl.js`/`sdl.wasm` are SDL3
compiled with Emscripten, shipped inside the bundler; the Go program is a separate wasm
module that calls into it through `syscall/js`. The game embeds no images, fonts or
sounds, so there are no assets to serve alongside.

Browser-specific behaviour lives in [`platform_js.go`](platform_js.go) (with
[`platform_notjs.go`](platform_notjs.go) as its native twin): the window is created at
the browser viewport size, `SetVSync` is skipped (it has no js binding, and
`requestAnimationFrame` already paces frames), and `-shot` is disabled since there is no
filesystem. Resizing the browser window takes effect on reload — SDL's web backend takes
the window size from the canvas at creation and there is no resize path.

## Running

```
agario                 # play
agario -demo           # AI drives the player, camera follows the leader
agario -headless       # simulate with no window, print stats and exit
agario -version        # print version and platform
```

| Flag | Meaning |
|---|---|
| `-version` | print the version and platform, then exit |
| `-seed N` | world seed (0 = time-based) |
| `-w`, `-h` | window size (default 1280×720) |
| `-demo` | autopilot; also toggled in-game with `T` |
| `-warmup N` | simulate N ticks before the first frame |
| `-headless` | no window; run `-ticks N` and print a summary |
| `-shot FILE.bmp` | render one frame to a file and exit |
| `-record FILE` | record the session (`.jsonl`, or `.jsonl.gz` to compress) |
| `-replay FILE` | play a recording back instead of playing |
| `-speed N` | replay speed multiplier (default 1) |
| `-checksum-every N` | ticks between recorded state checksums (0 disables) |

### Controls

| Input | Action |
|---|---|
| Mouse | steer |
| `Space` | split |
| `W` | eject mass |
| `P` | pause |
| `T` | toggle autopilot |
| `R` | respawn after death |
| `Esc` | quit |

## Recording and replaying a session

```bash
agario -record session.jsonl.gz          # play, and write the session out
agario -replay session.jsonl.gz          # watch it back
agario -replay session.jsonl.gz -headless   # verify it reproduces exactly
```

**A recording stores no game state.** It holds the seed and what the player did, and
replay rebuilds everything else — bots, food, viruses — by re-simulating. That works
because the simulation is deterministic: a fixed 120 Hz timestep, one seeded RNG on the
`World`, no wall-clock and no goroutines anywhere in `internal/game`. The whole input
surface is a steering target per tick plus split, eject, respawn and autopilot.

What gets recorded is the world-space target, not the mouse position: the camera smooths
its motion using real frame time, so the same pixel means different things on different
machines. Recording the derived target makes a replay independent of frame rate, window
size and display — a session recorded at 1280×720 replays correctly at any size.

Roughly 4 MB per hour gzipped. Playback controls are `Space` to pause, `←`/`→` to seek ten
seconds (hold `Shift` for a minute), `Home` to restart, `-`/`=` to halve or double speed
and `T` to switch between following the player and the leader. Seeking backwards
re-simulates from the start, because Go's `math/rand` state cannot be serialised — about
1.3 s to rewind ten minutes of game time.

Every 120 ticks the recording stores a hash of the whole world, so `-replay -headless`
reports not just *that* a replay diverged but where and in what:

```
tick 1920: recorded cd943251f46b4071, replayed 80b32db7cb7ffd13
    blobs   recorded 16, replayed 15
    deaths  recorded 1, replayed 0
```

That costs about 0.2% of a tick, so it is on by default; `-checksum-every 1` narrows a
divergence to the exact tick once you have one. **Bit-exact replay is promised for the
same binary on the same machine, and not across machines** — `math.Exp` and `math.Pow`
take CPU-feature-gated assembly paths on amd64 and differ by architecture and Go version.
The header records `goos`, `goarch` and the Go version, and a replay warns when they
differ rather than reporting the mismatch as a bug.

The format is newline-delimited JSON, one record per frame, gzipped when the filename ends
in `.gz` — the same idiom as the environment protocol, and readable with
`zcat session.jsonl.gz | head`:

```json
{"k":"hdr","format":"agario-replay","v":1,"seed":7,"tick_rate":120,...}
{"k":"t","t":0,"n":2,"x":3500,"y":2000,"a":["split"]}
{"k":"ck","t":120,"h":"c85bbf9ce0cf7057","time":1,"mass":388,"blobs":16,...}
```

`make replay-check` records a scripted headless session and verifies it, and CI runs the
same two commands on every push.

## Letting an agent play

The same game is available as a [Gymnasium](https://gymnasium.farama.org/) environment, so
a reinforcement-learning agent can play against the same bots, in the same arena, under
the same rules a human plays under. **The agent drives one cell with a heading and a
trigger**, which is what the mouse and the `Space`/`W` keys do — it is given no action a
player does not have, and it sees only what is within its view radius, the same horizon
the camera gives you.

The rules are not reimplemented in Python. A small Go binary serves the simulation over a
line-oriented JSON protocol on stdin/stdout, and the Python package is a client:

```sh
go build -o agario-env ./cmd/agario-env
pip install -e python
```

```python
import gymnasium, agario_gym

env = gymnasium.make("Agario-Small-v0")
obs, info = env.reset(seed=0)
obs, reward, terminated, truncated, info = env.step(env.action_space.sample())
```

The protocol is meant to be driveable by hand, which is also how to check the binary
works:

```console
$ ./agario-env
{"id":1,"cmd":"hello"}
{"id":2,"cmd":"reset","env_id":0,"seed":7}
{"id":3,"cmd":"step","env_id":0,"action":[4,1]}
```

Before training anything, find out what the numbers should look like:

```console
$ python python/examples/random_agent.py --episodes 5
policy    final mass  best mass  survived    reward
---------------------------------------------------
random           7.2       28.2     23.0s     -3.63
greedy          72.8       72.8     30.0s      1.07
```

`greedy` is twenty lines of heuristic that never splits: flee anything that can eat you,
otherwise eat the nearest thing you can. A learned policy that does not beat it has not
learned anything worth having. CI runs this comparison, because it is the only check that
catches the observation encoding or the heading mapping breaking in a way the unit tests
cannot see.

Many worlds share one server process, so a vectorised run costs one child process and one
round trip per batched step rather than N of each:

```python
envs = gymnasium.make_vec("Agario-Small-v0", num_envs=16)
```

That sustains roughly 3,000 agent steps per second at full population. `agario-env` links
no SDL — `internal/game` has no graphics dependency — so a training loop pulls in nothing
but the Go runtime, and the environment server is exactly the simulation the playable game
runs.

New to this? [README-AI.md](README-AI.md) is a step-by-step guide that assumes no prior
Gymnasium experience. [python/README.md](python/README.md) is the reference: the action
and observation spaces, the reward schemes, seeding, and the vectorised environment.

---

## How SDL is loaded

`go-sdl3` is a [purego](https://github.com/ebitengine/purego) binding: it `dlopen`s the
shared library at startup rather than linking it, so **there is no cgo and no SDL dev
headers are involved**, and cross-compiling needs nothing but `GOOS`/`GOARCH`.

`loadSDL` in `main.go` tries the system library first (`libSDL3.so.0`, `SDL3.dll`,
`libSDL3.dylib`) and falls back to the copy `binsdl` embeds in the binary. Preferring the
system copy picks up distro security updates; the fallback is what makes a downloaded
release run on a machine with no SDL3 at all. On Debian/Ubuntu the system copy is
`apt install libsdl3-0`.

One sharp edge: all 996 symbols are resolved eagerly at load and a missing one **panics**,
so an SDL3 older than the binding expects fails immediately rather than degrading.

No `SDL3_ttf` is required — all text uses SDL3's built-in 8×8 debug font.

## Layout

```
main.go               SDL setup, event loop, fixed-timestep accumulator, headless mode
platform_notjs.go     native window size, vsync, screenshot support
platform_js.go        the browser twin of the above
web/index.html        page shell for the WebAssembly build
internal/game/        the simulation — imports no SDL, so it is testable headlessly
  config.go           every tunable constant
  entity.go           Blob, Owner, Food, Ejecta, Virus; mass/radius/speed formulas
  world.go            World.Step, population maintenance, leaderboard
  physics.go          movement integration, impulse decay, mass decay
  grid.go             uniform spatial hash (broad phase)
  collide.go          the interaction table from spec section 4
  actions.go          split, eject, virus pop
  bot.go              AI steering
internal/render/      all remaining SDL calls
  circle.go           the antialiased disc texture every cell is drawn with
  camera.go           world<->screen, zoom, smoothing, arena clamp
  draw.go             world, HUD, leaderboard, minimap
  text.go             scaled debug-font helpers
internal/replay/      session record and replay (no SDL)
  format.go           the on-disk records, and the determinism contract
  recorder.go         appends a session; a nil *Recorder is "not recording"
  player.go           re-drives a world from a log, seeking and divergences
  checksum.go         the state hash that proves a replay matched
cmd/agario-env/       the RL environment server (no SDL)
internal/agarienv/    its JSON-lines protocol, sessions and dispatch
python/               the Gymnasium client — see README-AI.md
```

### Design notes

**Ownership.** A cell is not the unit of ownership. Splitting means one participant
controls up to 16 circles, so `Owner` holds `[]*Blob`. The human and every bot are both
`Owner`s and differ only in whether a `Brain` is attached — which is all autopilot mode
does. Split, eject, merge, camera centering and the leaderboard are written once.

**Units.** Every constant is in world units per *second*. `description.md` quotes speeds
per tick at 60fps (`Ks = 2.2`, `Smin = 1.0`), which needs a `dt * 60` factor to work with
a per-second `dt`; mixing the two conventions yields a game 60× too slow, and because the
floor clamps everything below mass ≈ 12000, the whole speed curve would vanish into it.

**Fixed timestep.** The simulation always advances in `TickDT` (1/120 s) increments
regardless of frame rate, so `Step` is deterministic and reproducible in tests.

**Rendering.** Every cell and pellet is one 256×256 antialiased white disc texture,
color-modulated and scaled — two draws per cell (dark rim, then fill) give the agar.io
look from a single texture. Viruses can't be expressed that way and use `RenderGeometry`
with an alternating inner/outer rim. Note that `RenderGeometryRaw` is unusable for
untextured geometry in this binding: it derives the vertex count from `len(uv)/2`, so
passing `uv == nil` silently draws nothing.

### Deviation from the spec

Spec section 4 gives the cell-versus-virus condition as `distance ≤ R_A + R_virus`, i.e.
merely touching a virus pops you. That makes a virus an unavoidable landmine with an
effective radius of `R_virus + R_you`. This implementation uses the same overlap rule as
cell consumption, `distance ≤ R_A − 0.33·R_virus`, which matches the original game and
leaves viruses dodgeable. Everything else follows the spec as written.

## Tests

```
go test ./...                                   # unit + integration
go test ./internal/game -bench=Step -benchtime=3s
go run . -headless -ticks 30000                 # smoke: panics, NaNs, populations
```

`Step` costs **~18.5 µs per tick** with no allocations of its own, against an 8.3 ms tick
budget at 120 Hz — about 0.2% of one core. Measured on an Intel Core Ultra 7 165H,
`-benchtime=120000x`, seed 17, world warmed 100 simulated seconds and recycled every 3000
ticks; two runs gave 18527 and 19047 ns/op, and a 60000x run gave 18576. Headless
throughput is ~39 000 ticks/s, roughly 325× real time.

The recycling matters. Benchmarking one continuously-running world makes the result
depend on `b.N`: cells keep growing and splitting, per-tick cost climbs with the blob
count, and runs at different iteration counts are not comparable — an early version of
this benchmark reported anywhere from 5 to 40 µs/op for that reason. The `17 B/op` the
benchmark prints is the periodic world rebuild, which is excluded from the timer but not
from the allocation counter.

Coverage includes the mass/radius/speed formulas, both consumption boundaries, split and
merge mass conservation and the blob cap, virus popping and reproduction, grid queries
against a brute-force scan, arena containment, framerate-independent decay, and
determinism across 10 000 steps from a fixed seed.

Integration tests drive the same behaviours through the real `Step` pipeline rather than
calling the resolvers directly. That is worth the duplication: it is what caught the soft
push being applied to merge-ready blobs (which pinned split halves just outside the merge
threshold, so they never recombined) and bots steering at grid-bucket centers instead of
actual pellets (so they parked on empty ground and starved).

Tests that need an isolated scenario set `FoodTarget`/`BotTarget`/`VirusTarget` to zero
to stop `maintain()` repopulating the arena underneath them.

## License

Copyright (c) 2026 Christophe Pallier.

Licensed under the Apache License, Version 2.0 — see [LICENSE](LICENSE) and
[NOTICE](NOTICE). Every source file carries an
`SPDX-License-Identifier: Apache-2.0` header.
