// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

// Command agario-env serves the agario simulation to an external process so an
// RL agent can play it. It speaks one JSON object per line on stdin and stdout;
// see internal/agarienv for the wire format, and python/ for a Gymnasium client.
//
// Anything this program has to say goes to stderr: stdout carries the protocol
// and nothing else.
//
// It is driveable by hand:
//
//	$ agario-env
//	{"id":1,"cmd":"hello"}
//	{"id":2,"cmd":"reset","env_id":0,"seed":7}
//	{"id":3,"cmd":"step","env_id":0,"action":[4,1]}
//
// This binary links no SDL: internal/game has no graphics dependency, so a
// training run pulls in nothing but the Go runtime.
package main

import (
	"flag"
	"fmt"
	"os"

	"agario/internal/agarienv"
)

func main() {
	def := agarienv.DefaultOptions()

	var (
		frames    = flag.Int("frames", def.Frames, "simulation ticks advanced per step command")
		kFood     = flag.Int("k-food", def.KFood, "food pellets reported per observation")
		kCells    = flag.Int("k-cells", def.KCells, "enemy cells reported per observation")
		kVirus    = flag.Int("k-virus", def.KVirus, "viruses reported per observation")
		kEjecta   = flag.Int("k-ejecta", def.KEjecta, "ejected pellets reported per observation")
		viewScale = flag.Float64("view-scale", def.ViewScale, "view radius as a multiple of the agent's radius")
		food      = flag.Int("food", def.Food, "food pellets in the arena")
		bots      = flag.Int("bots", def.Bots, "AI opponents")
		viruses   = flag.Int("viruses", def.Viruses, "viruses in the arena")
		version   = flag.Bool("version", false, "print the protocol version and exit")
	)
	flag.Parse()

	if *version {
		fmt.Fprintf(os.Stderr, "agario-env protocol %d\n", agarienv.Protocol)
		return
	}

	opt := agarienv.Options{
		Frames:    *frames,
		KFood:     *kFood,
		KCells:    *kCells,
		KVirus:    *kVirus,
		KEjecta:   *kEjecta,
		ViewScale: *viewScale,
		Food:      *food,
		Bots:      *bots,
		Viruses:   *viruses,
	}
	if err := validate(opt); err != nil {
		fmt.Fprintln(os.Stderr, "agario-env:", err)
		os.Exit(2)
	}

	if err := agarienv.New(opt).Run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "agario-env:", err)
		os.Exit(1)
	}
}

// validate rejects settings that would produce a malformed observation space,
// which is far easier to diagnose here than as a shape mismatch in Python.
func validate(o agarienv.Options) error {
	switch {
	case o.Frames < 1:
		return fmt.Errorf("-frames must be at least 1, got %d", o.Frames)
	case o.KFood < 1 || o.KCells < 1 || o.KVirus < 1 || o.KEjecta < 1:
		return fmt.Errorf("every -k-* must be at least 1")
	case o.ViewScale <= 0:
		return fmt.Errorf("-view-scale must be positive, got %v", o.ViewScale)
	case o.Food < 0 || o.Bots < 0 || o.Viruses < 0:
		return fmt.Errorf("-food, -bots and -viruses cannot be negative")
	}
	return nil
}
