// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package replay

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand"
	"strings"
	"testing"

	"agario/internal/game"
)

// farmTicks is how long the player rides the autopilot before the scripted
// actions start. A fresh player has 25 mass, and Split needs 36 and Eject 35,
// so acting any earlier records nothing but no-ops — which replay reproduces
// perfectly and which therefore test nothing. Autopilot reaches 45 by tick 500.
const farmTicks = 600

// script drives a world the way the game loop does: actions first, reading the
// target left over from the previous tick, then this tick's target, then Step.
// Getting that order wrong is the one mistake that makes a replay desync, so
// the fixture has to model it exactly.
func script(t *testing.T, rec *Recorder, w *game.World, ticks int) {
	t.Helper()
	for i := 0; i < ticks; i++ {
		switch i {
		case 0:
			// Farm mass, so the splits and ejects below are real.
			w.SetAutopilot(true)
			rec.Action(ActAutopilotOn)
		case farmTicks:
			w.SetAutopilot(false)
			rec.Action(ActAutopilotOff)
		}
		if i > farmTicks && i%900 == 450 {
			w.Split(w.Player)
			rec.Action(ActSplit)
		}
		if i > farmTicks && i%300 == 100 {
			w.Eject(w.Player)
			rec.Action(ActEject)
		}
		if w.Player.Dead {
			w.RespawnPlayer()
			rec.Action(ActRespawn)
		}

		tt := float64(i) * game.TickDT
		x := game.WorldSize/2 + math.Cos(tt*0.7)*1500
		y := game.WorldSize/2 + math.Sin(tt*0.5)*1500

		rec.Tick(x, y)
		w.SetPlayerTarget(x, y)
		w.Step(game.TickDT)
		rec.EndTick(w)
	}
}

func record(t *testing.T, seed int64, ticks, checksumEvery int) ([]byte, uint64) {
	t.Helper()
	sink := &MemSink{}
	rec, err := NewRecorder(sink, Header{Seed: seed, ChecksumEvery: checksumEvery})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	w := game.NewWorld(seed)
	script(t, rec, w, ticks)
	if err := rec.Close(w); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return sink.Bytes(), Checksum(w)
}

// replayAll replays a log to the end and returns the final world.
func replayAll(t *testing.T, log []byte) (*Player, *game.World) {
	t.Helper()
	p, err := Open(bytes.NewReader(log))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w := p.NewWorld()
	for p.Advance(w) {
	}
	return p, w
}

// TestRecordReplayRoundTrip is the test this package lives or dies by: a
// recorded session must replay to a bit-identical world. A checkpoint on every
// tick means a divergence is caught the moment it appears rather than up to a
// second later.
func TestRecordReplayRoundTrip(t *testing.T) {
	const ticks = 20000
	log, want := record(t, 7, ticks, 1)

	p, w := replayAll(t, log)
	if p.Truncated() {
		t.Error("log reported truncated, but it was closed cleanly")
	}
	if p.Ticks() != ticks {
		t.Errorf("replayed log has %d ticks, recorded %d", p.Ticks(), ticks)
	}
	if divs := p.Divergences(); len(divs) > 0 {
		t.Errorf("replay diverged at %d checkpoints; first:\n%v", len(divs), divs[0])
	}
	if got := Checksum(w); got != want {
		t.Errorf("final world differs: recorded %016x, replayed %016x", want, got)
	}
}

// TestReplayDetectsDivergence proves the checksum fires. A check that never
// fails is worse than no check, because it is trusted.
func TestReplayDetectsDivergence(t *testing.T) {
	// A purpose-built session rather than the shared script: the split being
	// dropped has to be one that actually did something, and the assertions
	// below check that rather than assuming it.
	sink := &MemSink{}
	rec, err := NewRecorder(sink, Header{Seed: 7, ChecksumEvery: 1})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	w := game.NewWorld(7)

	w.SetAutopilot(true)
	rec.Action(ActAutopilotOn)
	for i := 0; i < farmTicks; i++ {
		rec.Tick(2500, 2500)
		w.SetPlayerTarget(2500, 2500)
		w.Step(game.TickDT)
		rec.EndTick(w)
	}
	w.SetAutopilot(false)
	rec.Action(ActAutopilotOff)

	before := len(w.Player.Blobs)
	w.Split(w.Player)
	rec.Action(ActSplit)
	if len(w.Player.Blobs) == before {
		t.Fatalf("the split was a no-op at %.1f mass, so dropping it would "+
			"prove nothing", w.Player.Mass())
	}
	at := rec.Ticks() // the split was applied immediately before this tick

	for i := 0; i < 400; i++ {
		rec.Tick(3000, 2000)
		w.SetPlayerTarget(3000, 2000)
		w.Step(game.TickDT)
		rec.EndTick(w)
	}
	if err := rec.Close(w); err != nil {
		t.Fatalf("Close: %v", err)
	}
	log := sink.Bytes()

	// Drop that split. It shares a tick with the autopilot_off that preceded
	// it — both queued before the same tick — so what gets removed is the
	// element, not the whole array. Replay then runs an identical session minus
	// one action, which perturbs the shared RNG cursor from that tick onward.
	const drop = `,"split"]`
	tampered := strings.Replace(string(log), drop, "]", 1)
	if tampered == string(log) {
		t.Fatalf("no %s in the recording to drop", drop)
	}
	p, _ := replayAll(t, []byte(tampered))
	divs := p.Divergences()
	if len(divs) == 0 {
		t.Fatal("dropped an action and replay still reported no divergence")
	}
	// With a checkpoint on every tick, the one right after the missing split
	// has to catch it.
	if divs[0].Tick != at+1 {
		t.Errorf("first divergence at tick %d, want %d", divs[0].Tick, at+1)
	}
	// And the witness has to say what happened, not merely that something did.
	if divs[0].WantW.Blobs == divs[0].GotW.Blobs {
		t.Errorf("witness did not show the missing split: %+v", divs[0])
	}
}

// TestActionOrderIsPreserved pins the ordering guarantee: split,eject and
// eject,split are different sessions, and each must replay as itself.
func TestActionOrderIsPreserved(t *testing.T) {
	build := func(order []Action) ([]byte, uint64) {
		sink := &MemSink{}
		rec, err := NewRecorder(sink, Header{Seed: 3, ChecksumEvery: 1})
		if err != nil {
			t.Fatalf("NewRecorder: %v", err)
		}
		w := game.NewWorld(3)

		// Farm past the split and eject mass thresholds first; below them both
		// actions are no-ops in either order and the test proves nothing.
		w.SetAutopilot(true)
		rec.Action(ActAutopilotOn)
		for i := 0; i < farmTicks; i++ {
			rec.Tick(2500, 2500)
			w.SetPlayerTarget(2500, 2500)
			w.Step(game.TickDT)
			rec.EndTick(w)
		}
		w.SetAutopilot(false)
		rec.Action(ActAutopilotOff)
		if m := w.Player.Mass(); m < game.SplitMinMass {
			t.Fatalf("player has %.1f mass, below the %.0f split threshold: "+
				"the fixture would record no-ops", m, game.SplitMinMass)
		}

		for i := 0; i < 400; i++ {
			rec.Tick(2500, 2500)
			w.SetPlayerTarget(2500, 2500)
			w.Step(game.TickDT)
			rec.EndTick(w)
		}
		for _, a := range order {
			switch a {
			case ActSplit:
				w.Split(w.Player)
			case ActEject:
				w.Eject(w.Player)
			}
			rec.Action(a)
		}
		for i := 0; i < 400; i++ {
			rec.Tick(3000, 2000)
			w.SetPlayerTarget(3000, 2000)
			w.Step(game.TickDT)
			rec.EndTick(w)
		}
		if err := rec.Close(w); err != nil {
			t.Fatalf("Close: %v", err)
		}
		return sink.Bytes(), Checksum(w)
	}

	logSE, sumSE := build([]Action{ActSplit, ActEject})
	logES, sumES := build([]Action{ActEject, ActSplit})

	if sumSE == sumES {
		t.Fatal("split,eject and eject,split produced identical worlds; " +
			"this test cannot detect an ordering bug")
	}
	for _, tc := range []struct {
		name string
		log  []byte
		want uint64
	}{{"split,eject", logSE, sumSE}, {"eject,split", logES, sumES}} {
		p, w := replayAll(t, tc.log)
		if divs := p.Divergences(); len(divs) > 0 {
			t.Errorf("%s: replay diverged: %v", tc.name, divs[0])
		}
		if got := Checksum(w); got != tc.want {
			t.Errorf("%s: replayed %016x, recorded %016x", tc.name, got, tc.want)
		}
	}
}

// TestZeroTickFrameCarriesActions covers the high-refresh-rate case: a frame
// that runs no ticks can still apply actions, and they must land before the
// next tick that does run rather than being dropped or reordered.
func TestZeroTickFrameCarriesActions(t *testing.T) {
	sink := &MemSink{}
	rec, err := NewRecorder(sink, Header{Seed: 5, ChecksumEvery: 1})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	w := game.NewWorld(5)
	for i := 0; i < 10; i++ {
		rec.Tick(2500, 2500)
		w.SetPlayerTarget(2500, 2500)
		w.Step(game.TickDT)
		rec.EndTick(w)
	}
	// A frame that polled input and ran zero ticks.
	w.Split(w.Player)
	rec.Action(ActSplit)
	// The next frame that does run ticks, at the same target as before, so the
	// coalescer would happily have extended the previous run.
	for i := 0; i < 10; i++ {
		rec.Tick(2500, 2500)
		w.SetPlayerTarget(2500, 2500)
		w.Step(game.TickDT)
		rec.EndTick(w)
	}
	if err := rec.Close(w); err != nil {
		t.Fatalf("Close: %v", err)
	}

	p, w2 := replayAll(t, sink.Bytes())
	if divs := p.Divergences(); len(divs) > 0 {
		t.Errorf("replay diverged: %v", divs[0])
	}
	if got, want := Checksum(w2), Checksum(w); got != want {
		t.Errorf("replayed %016x, recorded %016x", got, want)
	}

	// The action must be recorded against tick 10, not folded into the run
	// that ended at tick 9.
	var found bool
	for _, f := range p.frames {
		if len(f.Actions) > 0 {
			if f.Tick != 10 {
				t.Errorf("action recorded at tick %d, want 10", f.Tick)
			}
			found = true
		}
	}
	if !found {
		t.Error("the queued action was dropped")
	}
}

// TestTrailingActionsAreRecorded covers quitting right after an action: it
// changed the world the closing checksum was taken from, so a replay that never
// applied it would report a divergence that never happened.
func TestTrailingActionsAreRecorded(t *testing.T) {
	sink := &MemSink{}
	rec, err := NewRecorder(sink, Header{Seed: 11, ChecksumEvery: 0})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	w := game.NewWorld(11)
	for i := 0; i < 50; i++ {
		rec.Tick(2500, 2500)
		w.SetPlayerTarget(2500, 2500)
		w.Step(game.TickDT)
		rec.EndTick(w)
	}
	w.Split(w.Player) // then quit, with no tick after it
	rec.Action(ActSplit)
	if err := rec.Close(w); err != nil {
		t.Fatalf("Close: %v", err)
	}

	p, w2 := replayAll(t, sink.Bytes())
	if divs := p.Divergences(); len(divs) > 0 {
		t.Errorf("trailing action lost: %v", divs[0])
	}
	if got, want := Checksum(w2), Checksum(w); got != want {
		t.Errorf("replayed %016x, recorded %016x", got, want)
	}
}

// TestFloatRoundTrip pins the assumption the whole text format rests on: a
// float64 survives encoding and decoding with every bit intact.
func TestFloatRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	values := make([]float64, 0, 10000)
	for len(values) < 10000 {
		v := math.Float64frombits(rng.Uint64())
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue // the recorder rejects these on purpose
		}
		values = append(values, v)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, v := range values {
		if err := enc.Encode(Frame{Kind: KindFrame, X: v, Y: -v, N: 1}); err != nil {
			t.Fatalf("encoding %v: %v", v, err)
		}
	}

	dec := json.NewDecoder(&buf)
	for i, want := range values {
		var f Frame
		if err := dec.Decode(&f); err != nil {
			t.Fatalf("decoding record %d: %v", i, err)
		}
		if math.Float64bits(f.X) != math.Float64bits(want) {
			t.Fatalf("record %d: X round-tripped %x, want %x",
				i, math.Float64bits(f.X), math.Float64bits(want))
		}
		if math.Float64bits(f.Y) != math.Float64bits(-want) {
			t.Fatalf("record %d: Y round-tripped %x, want %x",
				i, math.Float64bits(f.Y), math.Float64bits(-want))
		}
	}
}

// TestTruncatedLogReplaysPrefix covers a killed process: what was flushed must
// still replay, and the reader must say the tail is missing rather than
// pretending the session ended there.
func TestTruncatedLogReplaysPrefix(t *testing.T) {
	log, _ := record(t, 7, 2000, 10)

	cut := log[:len(log)*2/3]
	// Land mid-record rather than on a line boundary, which is what a killed
	// writer actually leaves behind.
	if i := bytes.LastIndexByte(cut, '\n'); i > 0 && i < len(cut)-1 {
		cut = cut[:i+1+len(cut[i+1:])/2]
	}

	p, err := Open(bytes.NewReader(cut))
	if err != nil {
		t.Fatalf("Open on a truncated log: %v", err)
	}
	if !p.Truncated() {
		t.Error("truncated log did not report itself truncated")
	}
	if p.Ticks() == 0 {
		t.Fatal("truncated log replayed no ticks at all")
	}
	w := p.NewWorld()
	for p.Advance(w) {
	}
	if divs := p.Divergences(); len(divs) > 0 {
		t.Errorf("the surviving prefix diverged: %v", divs[0])
	}
}

// TestNilRecorderIsUsable is what lets every call site skip an `if recording`
// branch.
func TestNilRecorderIsUsable(t *testing.T) {
	var rec *Recorder
	w := game.NewWorld(1)

	rec.Action(ActSplit)
	rec.Tick(1, 2)
	rec.EndTick(w)
	if got := rec.Ticks(); got != 0 {
		t.Errorf("nil recorder reported %d ticks", got)
	}
	if err := rec.Err(); err != nil {
		t.Errorf("nil recorder reported an error: %v", err)
	}
	if err := rec.Close(w); err != nil {
		t.Errorf("nil recorder failed to close: %v", err)
	}

	// An empty path is the "not recording" case, and must reach the same place.
	sink, err := Create("")
	if err != nil {
		t.Fatalf("Create(\"\"): %v", err)
	}
	rec2, err := NewRecorder(sink, Header{})
	if err != nil {
		t.Fatalf("NewRecorder(nil sink): %v", err)
	}
	if rec2 != nil {
		t.Error("an empty path produced a live recorder")
	}
}

// TestRejectsNonReplayFile keeps a wrong -replay argument from being reported
// as a desync.
func TestRejectsNonReplayFile(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty", ""},
		{"not json", "hello\n"},
		{"json but not a header", `{"k":"t","t":0,"n":1,"x":1,"y":2}` + "\n"},
		{"wrong format", `{"k":"hdr","format":"something-else","v":1}` + "\n"},
		{"future version", `{"k":"hdr","format":"agario-replay","v":99}` + "\n"},
	} {
		if _, err := Open(strings.NewReader(tc.body)); err == nil {
			t.Errorf("%s: Open accepted it", tc.name)
		}
	}
}

// TestSeekRewindsExactly checks the expensive half of seeking: rewinding
// rebuilds from the seed, and must land on the same state the forward pass had.
func TestSeekRewindsExactly(t *testing.T) {
	log, _ := record(t, 7, 3000, 100)

	p, err := Open(bytes.NewReader(log))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w := p.NewWorld()

	w = p.Seek(w, 1500)
	at1500 := Checksum(w)

	w = p.Seek(w, 2500) // forward: just advances
	w = p.Seek(w, 1500) // backward: rebuilds from the seed
	if got := Checksum(w); got != at1500 {
		t.Errorf("rewind landed on %016x, forward pass had %016x", got, at1500)
	}
	if p.Tick() != 1500 {
		t.Errorf("after seeking to 1500 the position is %d", p.Tick())
	}
	if divs := p.Divergences(); len(divs) > 0 {
		t.Errorf("seeking diverged: %v", divs[0])
	}
}
