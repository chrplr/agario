# agario

A single-player [agar.io](https://en.wikipedia.org/wiki/Agar.io) clone in Go, rendered with
SDL3 via [`Zyko0/go-sdl3`](https://github.com/Zyko0/go-sdl3). You play one cell against
AI bots in a 4000×4000 arena, with food, splitting, mass ejection and viruses.

Implements the specification in [`description.md`](description.md).

## Running

```
go run .                 # play
go run . -demo           # AI drives the player, camera follows the leader
go run . -headless       # simulate with no window, print stats and exit
```

| Flag | Meaning |
|---|---|
| `-seed N` | world seed (0 = time-based) |
| `-w`, `-h` | window size (default 1280×720) |
| `-demo` | autopilot; also toggled in-game with `T` |
| `-warmup N` | simulate N ticks before the first frame |
| `-headless` | no window; run `-ticks N` and print a summary |
| `-shot FILE.bmp` | render one frame to a file and exit |

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

## Requirements

Go 1.25+ and SDL3 at runtime (`libSDL3.so.0`; on Debian/Ubuntu, `apt install libsdl3-0`).

`go-sdl3` is a [purego](https://github.com/ebitengine/purego) binding: it `dlopen`s the
shared library at startup, so **there is no cgo and no SDL dev headers are needed**. All
996 symbols are resolved eagerly at load and a missing one panics, so an SDL3 older than
the binding expects will fail immediately rather than degrade. To bundle SDL instead of
using the system copy, replace the `sdl.LoadLibrary` call in `main.go` with
`defer binsdl.Load().Unload()`.

No `SDL3_ttf` is required — all text uses SDL3's built-in 8×8 debug font.

## Layout

```
main.go               SDL setup, event loop, fixed-timestep accumulator, headless mode
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
