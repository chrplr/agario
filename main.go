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
	"time"

	"agario/internal/game"
	"agario/internal/render"
	"github.com/Zyko0/go-sdl3/sdl"
)

var (
	flagHeadless = flag.Bool("headless", false, "run the simulation with no window and exit")
	flagTicks    = flag.Int("ticks", 20000, "number of ticks to run in headless mode")
	flagSeed     = flag.Int64("seed", 0, "world seed (0 means time-based)")
	flagWidth    = flag.Int("w", 1280, "window width")
	flagHeight   = flag.Int("h", 720, "window height")
	flagShot     = flag.String("shot", "", "render one frame to this .bmp and exit")
	flagWarmup   = flag.Int("warmup", 0, "ticks to simulate before the first frame")
	flagDemo     = flag.Bool("demo", false, "let the AI drive the player and follow the leader")
)

func main() {
	flag.Parse()

	seed := *flagSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}

	if *flagHeadless {
		runHeadless(seed, *flagTicks)
		return
	}
	if err := run(seed); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// runHeadless steps the world with no rendering. Useful for catching panics and
// NaNs, and for timing the simulation independently of the GPU.
func runHeadless(seed int64, ticks int) {
	w := game.NewWorld(seed)
	start := time.Now()
	for i := 0; i < ticks; i++ {
		// Drive the player around so its code paths are exercised too.
		t := float64(i) * game.TickDT
		w.SetPlayerTarget(
			game.WorldSize/2+math.Cos(t*0.7)*1500,
			game.WorldSize/2+math.Sin(t*0.5)*1500,
		)
		if i%900 == 450 {
			w.Split(w.Player)
		}
		if i%300 == 100 {
			w.Eject(w.Player)
		}
		if w.Player.Dead {
			w.RespawnPlayer()
		}
		w.Step(game.TickDT)
	}
	elapsed := time.Since(start)

	fmt.Printf("%d ticks (%.1f sim-seconds) in %v — %.0f ticks/sec\n",
		ticks, float64(ticks)*game.TickDT, elapsed.Round(time.Millisecond),
		float64(ticks)/elapsed.Seconds())
	fmt.Printf("food=%d ejecta=%d viruses=%d blobs=%d deaths=%d\n",
		len(w.Food), len(w.Ejectas), len(w.Viruses), w.TotalBlobs(), w.PlayerDeaths)
	fmt.Println("leaderboard:")
	for i, e := range w.Leaderboard(5) {
		fmt.Printf("  %d. %-12s %6.0f\n", i+1, e.Name, e.Mass)
	}
}

func run(seed int64) error {
	// The system libSDL3.so.0 is used directly; go-sdl3 is a purego binding, so
	// no cgo and no dev headers are involved. Swap in binsdl.Load() to embed the
	// library instead.
	if err := sdl.LoadLibrary(sdl.Path()); err != nil {
		return fmt.Errorf("loading %s (try: apt install libsdl3-0): %w", sdl.Path(), err)
	}
	defer sdl.CloseLibrary()

	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		return fmt.Errorf("sdl init: %w", err)
	}
	defer sdl.Quit()

	window, renderer, err := sdl.CreateWindowAndRenderer(
		"agar.io — Go + SDL3", *flagWidth, *flagHeight, sdl.WINDOW_RESIZABLE)
	if err != nil {
		return fmt.Errorf("creating window: %w", err)
	}
	defer window.Destroy()
	defer renderer.Destroy()

	renderer.SetVSync(1)

	outW, outH, err := renderer.RenderOutputSize()
	if err != nil {
		outW, outH = int32(*flagWidth), int32(*flagHeight)
	}

	cam := render.NewCamera(outW, outH)
	rd, err := render.New(renderer, cam)
	if err != nil {
		return fmt.Errorf("creating renderer resources: %w", err)
	}
	defer rd.Destroy()

	w := game.NewWorld(seed)
	if *flagDemo {
		w.SetAutopilot(true)
	}

	// Warm up so a screenshot or a fresh session starts on a lively arena
	// rather than 16 identical starting cells.
	for i := 0; i < *flagWarmup; i++ {
		w.SetPlayerTarget(w.Player.Center())
		w.Step(game.TickDT)
		if w.Player.Dead {
			w.RespawnPlayer()
		}
	}

	// The camera subject: the player normally, the current leader in demo mode.
	subject := func() *game.Owner {
		if w.Autopilot() {
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
	if *flagShot != "" {
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

	// Fixed-timestep accumulator: the simulation always advances in TickDT
	// increments, so physics does not change with frame rate and Step stays
	// reproducible between a 60Hz and a 144Hz display.
	var accumulator float64
	last := time.Now()
	paused := false

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
				switch k.Scancode {
				case sdl.SCANCODE_ESCAPE:
					return sdl.EndLoop
				case sdl.SCANCODE_SPACE:
					if !paused && !w.Player.Dead {
						w.Split(w.Player)
					}
				case sdl.SCANCODE_W:
					if !paused && !w.Player.Dead {
						w.Eject(w.Player)
					}
				case sdl.SCANCODE_P:
					paused = !paused
				case sdl.SCANCODE_R:
					if w.Player.Dead {
						w.RespawnPlayer()
						cam.Snap(subject())
					}
				case sdl.SCANCODE_T:
					w.SetAutopilot(!w.Autopilot())
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
			accumulator += frameTime
			for accumulator >= game.TickDT {
				wx, wy := cam.ScreenToWorld(mouseX, mouseY)
				w.SetPlayerTarget(wx, wy)
				w.Step(game.TickDT)
				accumulator -= game.TickDT
			}
			// Under autopilot nobody is going to press R.
			if w.Autopilot() && w.Player.Dead {
				w.RespawnPlayer()
			}
		}

		cam.Follow(subject(), frameTime)

		fpsFrames++
		if d := time.Since(fpsSince).Seconds(); d >= 0.5 {
			fps = float64(fpsFrames) / d
			fpsFrames = 0
			fpsSince = time.Now()
		}

		rd.DrawWorld(w, fps, paused)
		return nil
	})
}
