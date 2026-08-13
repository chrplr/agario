// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

// Command agario is a single-player agar.io clone: one human player against
// AI bots in a bounded arena, with splitting, mass ejection and viruses.
//
// All simulation lives in internal/game and imports no SDL, so it can be run
// headlessly (-headless) for smoke tests and benchmarks.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"time"

	"agario/internal/game"
	"agario/internal/render"
	"agario/internal/replay"
	"github.com/Zyko0/go-sdl3/bin/binsdl"
	"github.com/Zyko0/go-sdl3/sdl"
)

// version is set at build time via -ldflags="-X main.version=...". Release
// builds stamp it with the git tag; it stays "dev" for a plain `go build`.
var version = "dev"

var (
	flagVersion  = flag.Bool("version", false, "print the version and exit")
	flagHeadless = flag.Bool("headless", false, "run the simulation with no window and exit")
	flagTicks    = flag.Int("ticks", 20000, "number of ticks to run in headless mode")
	flagSeed     = flag.Int64("seed", 0, "world seed (0 means time-based)")
	flagWidth    = flag.Int("w", 1280, "window width")
	flagHeight   = flag.Int("h", 720, "window height")
	flagShot     = flag.String("shot", "", "render one frame to this .bmp and exit")
	flagWarmup   = flag.Int("warmup", 0, "ticks to simulate before the first frame")
	flagDemo     = flag.Bool("demo", false, "let the AI drive the player and follow the leader")

	flagRecord   = flag.String("record", "", "record this session to FILE (.jsonl, or .jsonl.gz to compress)")
	flagReplay   = flag.String("replay", "", "replay a recorded session from FILE")
	flagSpeed    = flag.Float64("speed", 1, "replay speed multiplier")
	flagChecksum = flag.Int("checksum-every", replay.DefaultChecksumEvery,
		"ticks between recorded state checksums (0 disables)")
)

func main() {
	flag.Parse()

	if *flagVersion {
		fmt.Printf("agario %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return
	}

	// A recording carries its own seed, warmup and autopilot state in its
	// header and first ticks. Honouring these flags as well would silently
	// replay something other than what was recorded.
	if *flagReplay != "" {
		for _, bad := range []struct {
			name string
			set  bool
		}{
			{"-record", *flagRecord != ""},
			{"-seed", *flagSeed != 0},
			{"-warmup", *flagWarmup != 0},
			{"-demo", *flagDemo},
		} {
			if bad.set {
				fmt.Fprintf(os.Stderr, "error: -replay cannot be combined with %s\n", bad.name)
				os.Exit(1)
			}
		}
	}

	seed := *flagSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}

	var err error
	switch {
	case *flagReplay != "" && *flagHeadless:
		err = verifyReplay(*flagReplay)
	case *flagHeadless:
		err = runHeadless(seed, *flagTicks)
	default:
		err = run(seed)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// recordHeader describes this session for a recording. The seed passed here is
// the resolved one: -seed 0 means time-based, and storing the flag instead
// would make the recording unreplayable.
func recordHeader(seed int64, w *game.World, window []int) replay.Header {
	return replay.Header{
		Seed:          seed,
		Food:          w.FoodTarget,
		Bots:          w.BotTarget,
		Viruses:       w.VirusTarget,
		ChecksumEvery: *flagChecksum,
		GameVersion:   version,
		Window:        window,
	}
}

// verifyReplay re-simulates a recording and reports any divergence. It needs no
// window, so it is what CI runs; -replay on its own watches the session
// instead.
func verifyReplay(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	p, err := replay.Open(f)
	if err != nil {
		return err
	}
	h := p.Header()
	fmt.Printf("replaying %s: seed %d, %d ticks (%.1f sim-seconds), recorded by agario %s on %s/%s\n",
		path, h.Seed, p.Ticks(), float64(p.Ticks())*game.TickDT, h.GameVersion, h.GOOS, h.GOARCH)
	if p.Truncated() {
		fmt.Println("note: the recording has no end marker, so it was cut short; replaying what survived")
	}
	// Say this before any mismatch, so an expected floating-point difference is
	// not mistaken for a bug in the replay.
	if msg := p.Provenance(); msg != "" {
		fmt.Println("warning:", msg)
	}

	w := p.NewWorld()
	start := time.Now()
	for p.Advance(w) {
	}
	elapsed := time.Since(start)

	divs := p.Divergences()
	if len(divs) > 0 {
		fmt.Fprintf(os.Stderr, "%d of the recorded checkpoints did not match:\n", len(divs))
		for i, d := range divs {
			if i == 5 {
				fmt.Fprintf(os.Stderr, "  ... and %d more\n", len(divs)-i)
				break
			}
			fmt.Fprintf(os.Stderr, "  %v\n", d)
		}
		if h.ChecksumEvery > 1 {
			fmt.Fprintf(os.Stderr,
				"re-record with -checksum-every 1 to find the exact tick (this one used %d)\n",
				h.ChecksumEvery)
		}
		return fmt.Errorf("replay diverged from the recording")
	}

	if h.ChecksumEvery <= 0 {
		fmt.Printf("replayed %d ticks in %v, but the recording has no checkpoints, "+
			"so nothing was verified\n", p.Ticks(), elapsed.Round(time.Millisecond))
		return nil
	}
	fmt.Printf("replayed %d ticks in %v — every checkpoint matched\n",
		p.Ticks(), elapsed.Round(time.Millisecond))
	return nil
}

// runHeadless steps the world with no rendering. Useful for catching panics and
// NaNs, and for timing the simulation independently of the GPU.
//
// With -record it also writes a replay, which is how CI gets a fixture without
// a display. That is why the actions below run before the target rather than
// after it: the windowed loop applies actions in the event block, ahead of the
// tick that follows, and a recording only replays if this driver models the
// same order.
func runHeadless(seed int64, ticks int) error {
	w := game.NewWorld(seed)

	sink, err := replay.Create(*flagRecord)
	if err != nil {
		return err
	}
	rec, err := replay.NewRecorder(sink, recordHeader(seed, w, nil))
	if err != nil {
		return err
	}

	start := time.Now()
	for i := 0; i < ticks; i++ {
		if i%900 == 450 {
			w.Split(w.Player)
			rec.Action(replay.ActSplit)
		}
		if i%300 == 100 {
			w.Eject(w.Player)
			rec.Action(replay.ActEject)
		}
		if w.Player.Dead {
			w.RespawnPlayer()
			rec.Action(replay.ActRespawn)
		}

		// Drive the player around so its code paths are exercised too.
		t := float64(i) * game.TickDT
		tx := game.WorldSize/2 + math.Cos(t*0.7)*1500
		ty := game.WorldSize/2 + math.Sin(t*0.5)*1500

		rec.Tick(tx, ty)
		w.SetPlayerTarget(tx, ty)
		w.Step(game.TickDT)
		rec.EndTick(w)
	}
	elapsed := time.Since(start)

	if err := rec.Close(w); err != nil {
		return fmt.Errorf("writing %s: %w", *flagRecord, err)
	}

	fmt.Printf("%d ticks (%.1f sim-seconds) in %v — %.0f ticks/sec\n",
		ticks, float64(ticks)*game.TickDT, elapsed.Round(time.Millisecond),
		float64(ticks)/elapsed.Seconds())
	fmt.Printf("food=%d ejecta=%d viruses=%d blobs=%d deaths=%d\n",
		len(w.Food), len(w.Ejectas), len(w.Viruses), w.TotalBlobs(), w.PlayerDeaths)
	fmt.Println("leaderboard:")
	for i, e := range w.Leaderboard(5) {
		fmt.Printf("  %d. %-12s %6.0f\n", i+1, e.Name, e.Mass)
	}
	if *flagRecord != "" {
		fmt.Printf("recorded %d ticks to %s\n", rec.Ticks(), *flagRecord)
	}
	return nil
}

// loadSDL loads SDL3 and returns a cleanup function. go-sdl3 is a purego
// binding, so this is a dlopen at runtime: no cgo, no dev headers, and
// cross-compiling needs nothing but GOOS/GOARCH.
//
// The system library is preferred, so a distro-patched SDL is picked up and the
// embedded copy stays unused. Falling back to binsdl is what lets a downloaded
// release binary run on a machine with no SDL3 installed; it unpacks its own
// copy to a temp directory.
func loadSDL() func() {
	if err := sdl.LoadLibrary(sdl.Path()); err == nil {
		return func() { sdl.CloseLibrary() }
	}
	return binsdl.Load().Unload
}

func run(seed int64) error {
	defer loadSDL()()

	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		return fmt.Errorf("sdl init: %w", err)
	}
	defer sdl.Quit()

	winW, winH := initialSize()
	window, renderer, err := sdl.CreateWindowAndRenderer(
		"agar.io — Go + SDL3", winW, winH, sdl.WINDOW_RESIZABLE)
	if err != nil {
		return fmt.Errorf("creating window: %w", err)
	}
	defer window.Destroy()
	defer renderer.Destroy()

	setVSync(renderer)

	outW, outH, err := renderer.RenderOutputSize()
	if err != nil {
		outW, outH = int32(winW), int32(winH)
	}

	cam := render.NewCamera(outW, outH)
	rd, err := render.New(renderer, cam)
	if err != nil {
		return fmt.Errorf("creating renderer resources: %w", err)
	}
	defer rd.Destroy()

	var (
		w   *game.World
		rec *replay.Recorder
		rp  *replay.Player
	)

	// followLeader is the replay's own camera choice, kept separate from
	// autopilot: during playback T must move the view, never the simulation.
	followLeader := false

	if *flagReplay != "" {
		f, err := os.Open(*flagReplay)
		if err != nil {
			return err
		}
		rp, err = replay.Open(f)
		f.Close()
		if err != nil {
			return err
		}
		if msg := rp.Provenance(); msg != "" {
			fmt.Fprintln(os.Stderr, "warning:", msg)
		}
		if rp.Truncated() {
			fmt.Fprintln(os.Stderr, "note: the recording was cut short; playing what survived")
		}
		w = rp.NewWorld()
	} else {
		w = game.NewWorld(seed)

		sink, err := replay.Create(*flagRecord)
		if err != nil {
			return err
		}
		if rec, err = replay.NewRecorder(sink, recordHeader(seed, w, []int{int(outW), int(outH)})); err != nil {
			return err
		}
		defer func() {
			if err := rec.Close(w); err != nil {
				fmt.Fprintln(os.Stderr, "error writing the recording:", err)
			} else if *flagRecord != "" {
				fmt.Printf("recorded %d ticks to %s\n", rec.Ticks(), *flagRecord)
			}
		}()

		// Demo mode and warmup go into the recording rather than being
		// reproduced by the player: the log then describes the whole session
		// and replay has one code path.
		if *flagDemo {
			w.SetAutopilot(true)
			rec.Action(replay.ActAutopilotOn)
		}

		// Warm up so a screenshot or a fresh session starts on a lively arena
		// rather than 16 identical starting cells.
		for i := 0; i < *flagWarmup; i++ {
			tx, ty := w.Player.Center()
			rec.Tick(tx, ty)
			w.SetPlayerTarget(tx, ty)
			w.Step(game.TickDT)
			rec.EndTick(w)
			if w.Player.Dead {
				w.RespawnPlayer()
				rec.Action(replay.ActRespawn)
			}
		}
	}

	// The camera subject: the player normally, the current leader in demo mode.
	// During playback the choice is the viewer's, since a recorded session may
	// be worth watching from either end.
	subject := func() *game.Owner {
		if (rp == nil && w.Autopilot()) || (rp != nil && followLeader) {
			if l := w.Leader(); l != nil {
				return l
			}
		}
		return w.Player
	}
	cam.Snap(subject())

	// Headed screenshot mode: draw one frame, read it back through SDL and
	// save it. This verifies the real render path without depending on a
	// desktop screenshot tool.
	if *flagShot != "" && canSaveFiles() {
		if rp != nil {
			rd.SetPlayback(render.Playback{
				Replaying: true, Tick: rp.Tick(), Ticks: rp.Ticks(), Speed: *flagSpeed,
			})
		}
		// Read back before Present: after the swap the backbuffer is undefined.
		rd.Draw(w, 60, false)
		surface, err := renderer.ReadPixels(nil)
		if err != nil {
			return fmt.Errorf("reading pixels: %w", err)
		}
		defer surface.Destroy()
		if err := surface.SaveBMP(*flagShot); err != nil {
			return fmt.Errorf("saving %s: %w", *flagShot, err)
		}
		fmt.Printf("wrote %s\n", *flagShot)
		return nil
	}

	// Every mutation of the world goes through one of these, so it is not
	// possible to change the simulation without the recorder seeing it. They
	// record what actually reached the world, no-ops included.
	doSplit := func() {
		w.Split(w.Player)
		rec.Action(replay.ActSplit)
	}
	doEject := func() {
		w.Eject(w.Player)
		rec.Action(replay.ActEject)
	}
	doRespawn := func() {
		w.RespawnPlayer()
		rec.Action(replay.ActRespawn)
	}
	doAutopilot := func(on bool) {
		w.SetAutopilot(on)
		if on {
			rec.Action(replay.ActAutopilotOn)
		} else {
			rec.Action(replay.ActAutopilotOff)
		}
	}

	// Fixed-timestep accumulator: the simulation always advances in TickDT
	// increments, so physics does not change with frame rate and Step stays
	// reproducible between a 60Hz and a 144Hz display.
	var accumulator float64
	last := time.Now()
	paused := false
	speed := *flagSpeed
	atEnd := false

	// Mouse position in window pixels, converted to a world target each frame
	// (the camera moves even when the mouse does not).
	mouseX, mouseY := float64(outW)/2, float64(outH)/2

	var fps float64
	fpsFrames := 0
	fpsSince := time.Now()

	return sdl.RunLoop(func() error {
		var event sdl.Event
		for sdl.PollEvent(&event) {
			switch event.Type {
			case sdl.EVENT_QUIT:
				return sdl.EndLoop

			case sdl.EVENT_MOUSE_MOTION:
				m := event.MouseMotionEvent()
				mouseX, mouseY = float64(m.X), float64(m.Y)

			case sdl.EVENT_WINDOW_RESIZED:
				if nw, nh, err := renderer.RenderOutputSize(); err == nil {
					cam.Resize(nw, nh)
				}

			case sdl.EVENT_KEY_DOWN:
				k := event.KeyboardEvent()
				if k.Repeat {
					break
				}
				if k.Scancode == sdl.SCANCODE_ESCAPE {
					return sdl.EndLoop
				}
				if rp != nil {
					// Playback: the keys drive the view and the tape, never the
					// simulation. Anything that touched the world here would
					// desync the replay from what was recorded.
					switch k.Scancode {
					case sdl.SCANCODE_P, sdl.SCANCODE_SPACE:
						paused = !paused
					case sdl.SCANCODE_LEFT, sdl.SCANCODE_RIGHT:
						step := int(game.TickRate) * 10
						if k.Mod&sdl.KMOD_SHIFT != 0 {
							step = int(game.TickRate) * 60
						}
						if k.Scancode == sdl.SCANCODE_LEFT {
							step = -step
						}
						w = rp.Seek(w, rp.Tick()+step)
						atEnd = rp.Done()
						cam.Snap(subject())
					case sdl.SCANCODE_HOME:
						w = rp.Seek(w, 0)
						atEnd = false
						cam.Snap(subject())
					case sdl.SCANCODE_MINUS:
						speed = math.Max(0.125, speed/2)
					case sdl.SCANCODE_EQUALS:
						speed = math.Min(16, speed*2)
					case sdl.SCANCODE_0:
						speed = 1
					case sdl.SCANCODE_T:
						followLeader = !followLeader
						cam.Snap(subject())
					}
					break
				}
				switch k.Scancode {
				case sdl.SCANCODE_SPACE:
					if !paused && !w.Player.Dead {
						doSplit()
					}
				case sdl.SCANCODE_W:
					if !paused && !w.Player.Dead {
						doEject()
					}
				case sdl.SCANCODE_P:
					paused = !paused
				case sdl.SCANCODE_R:
					if w.Player.Dead {
						doRespawn()
						cam.Snap(subject())
					}
				case sdl.SCANCODE_T:
					doAutopilot(!w.Autopilot())
					cam.Snap(subject())
				}
			}
		}

		now := time.Now()
		frameTime := now.Sub(last).Seconds()
		last = now
		// Clamp so a stall (window drag, breakpoint) does not make the world
		// fast-forward through hundreds of ticks at once.
		if frameTime > 0.25 {
			frameTime = 0.25
		}

		if !paused {
			accumulator += frameTime * speed
			for accumulator >= game.TickDT {
				if rp != nil {
					if !rp.Advance(w) {
						// Hold on the last frame rather than quitting: the end
						// of a session is usually the interesting part.
						paused, atEnd = true, true
						accumulator = 0
						break
					}
				} else {
					// Computed once and handed to both the recorder and the
					// world: calling ScreenToWorld twice would agree only by
					// accident.
					wx, wy := cam.ScreenToWorld(mouseX, mouseY)
					rec.Tick(wx, wy)
					w.SetPlayerTarget(wx, wy)
					w.Step(game.TickDT)
					rec.EndTick(w)
				}
				accumulator -= game.TickDT
			}
			// Under autopilot nobody is going to press R.
			if rp == nil && w.Autopilot() && w.Player.Dead {
				doRespawn()
			}
		}

		cam.Follow(subject(), frameTime)

		fpsFrames++
		if d := time.Since(fpsSince).Seconds(); d >= 0.5 {
			fps = float64(fpsFrames) / d
			fpsFrames = 0
			fpsSince = time.Now()
		}

		pb := render.Playback{Recording: *flagRecord != "", Tick: rec.Ticks(), Speed: speed}
		if rp != nil {
			pb = render.Playback{
				Replaying: true,
				Tick:      rp.Tick(),
				Ticks:     rp.Ticks(),
				Speed:     speed,
				AtEnd:     atEnd,
			}
		}
		rd.SetPlayback(pb)

		rd.DrawWorld(w, fps, paused)
		return nil
	})
}
