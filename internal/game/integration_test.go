// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package game

import "testing"

// TestVirusPopThroughStep drives a large cell into a virus using the real Step
// pipeline, rather than calling popOnVirus directly. This is what catches a
// mistake in the collision predicate that a direct unit test would miss.
func TestVirusPopThroughStep(t *testing.T) {
	w := newBareWorld(t)
	o := w.Player
	b := w.addBlob(o, 1000, 1000, 3000)
	v := &Virus{X: 1600, Y: 1000, Mass: VirusMass}
	w.Viruses = []*Virus{v}

	before := o.Mass() + v.Mass
	popped := false
	for i := 0; i < 2000; i++ {
		o.TargetX, o.TargetY = 1600, 1000
		w.Step(TickDT)
		if len(o.Blobs) > 1 {
			popped = true
			break
		}
	}

	if !popped {
		t.Fatalf("cell of mass %v never popped on the virus (final blobs=%d, dist=%v)",
			3000.0, len(o.Blobs), dist(b.X, b.Y, v.X, v.Y))
	}
	// maintain() legitimately refills the virus population, so check that this
	// specific virus is gone rather than that the list is empty.
	for _, remaining := range w.Viruses {
		if remaining == v {
			t.Error("the virus that was hit survived being popped")
		}
	}
	if got := o.Mass(); got < before*0.99 || got > before*1.01 {
		t.Errorf("mass not conserved through pop: %v -> %v", before, got)
	}
	if len(o.Blobs) > MaxBlobs {
		t.Errorf("pop produced %d blobs, over cap %d", len(o.Blobs), MaxBlobs)
	}
}

// TestSplitThenMergeThroughStep verifies the whole split lifecycle end to end:
// the halves separate, stay separate for the cooldown, then recombine.
func TestSplitThenMergeThroughStep(t *testing.T) {
	w := newBareWorld(t)
	o := w.Player
	w.addBlob(o, 2000, 2000, 200)
	o.TargetX, o.TargetY = 2600, 2000

	before := o.Mass()
	w.Split(o)
	if len(o.Blobs) != 2 {
		t.Fatalf("split produced %d blobs", len(o.Blobs))
	}

	// Let the impulse carry them apart.
	for i := 0; i < 60; i++ {
		w.Step(TickDT)
	}
	if d := dist(o.Blobs[0].X, o.Blobs[0].Y, o.Blobs[1].X, o.Blobs[1].Y); d < 10 {
		t.Errorf("split halves did not separate: d=%v", d)
	}

	// Park them on top of each other and run past the cooldown.
	cooldown := MergeDelay(before / 2)
	steps := int((cooldown + 5) / TickDT)
	for i := 0; i < steps; i++ {
		o.TargetX, o.TargetY = 2000, 2000
		w.Step(TickDT)
		if len(o.Blobs) == 1 {
			break
		}
	}

	if len(o.Blobs) != 1 {
		t.Fatalf("halves never recombined after %.0fs (blobs=%d)", cooldown+5, len(o.Blobs))
	}
	// Mass decays above DecayMinMass, so allow for that rather than requiring
	// exact conservation across a 34-second run.
	if got := o.Mass(); got > before || got < before*0.9 {
		t.Errorf("mass after split/merge cycle = %v, want just under %v", got, before)
	}
}

// TestBotsGrowByEating is a behavioural check on the AI: over a couple of
// simulated minutes bots must actually harvest food. An AI that only reacts to
// threats and prey, leaving its target alone when neither is in range, passes
// every other test here while never growing at all.
func TestBotsGrowByEating(t *testing.T) {
	w := NewWorld(2024)
	start := 0.0
	for _, o := range w.Owners {
		if o.IsBot {
			start += o.Mass()
		}
	}

	for i := 0; i < 12000; i++ { // 100 simulated seconds
		w.Step(TickDT)
	}

	end := 0.0
	n := 0
	for _, o := range w.Owners {
		if o.IsBot {
			end += o.Mass()
			n++
		}
	}
	if end <= start*1.5 {
		t.Errorf("bots barely grew in 100s: %v -> %v across %d bots", start, end, n)
	}
}

// TestPlayerDeathAndRespawn checks the full death path through Step.
func TestPlayerDeathAndRespawn(t *testing.T) {
	w := newBareWorld(t)
	p := w.Player
	w.addBlob(p, 2000, 2000, 20)
	killer := w.newOwner("Killer", true)
	killer.Brain = &Brain{}
	w.addBlob(killer, 2000, 2000, 5000)

	for i := 0; i < 600 && !p.Dead; i++ {
		w.Step(TickDT)
	}
	if !p.Dead {
		t.Fatal("player was not eaten by a much larger cell")
	}

	w.RespawnPlayer()
	if p.Dead {
		t.Error("player still dead after respawn")
	}
	if len(p.Blobs) != 1 {
		t.Errorf("respawned with %d blobs, want 1", len(p.Blobs))
	}
	if p.Mass() != PlayerStartMass {
		t.Errorf("respawned with mass %v, want %v", p.Mass(), PlayerStartMass)
	}
	if w.PlayerDeaths != 1 {
		t.Errorf("death counter = %d, want 1", w.PlayerDeaths)
	}
}
