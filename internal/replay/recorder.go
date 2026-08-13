// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package replay

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"runtime"
	"time"

	"agario/internal/game"
)

// flushEvery bounds how much a killed process loses. A truncated final line
// reads as truncation rather than corruption, so an interrupted recording still
// replays up to its last flush.
const flushEvery = 512

// Recorder appends a session to a sink.
//
// A nil *Recorder discards everything, so a caller that is not recording needs
// no branch: every method tolerates a nil receiver.
type Recorder struct {
	enc     *json.Encoder
	w       io.WriteCloser
	flusher interface{ Flush() error }

	every   int
	tick    int
	records int
	err     error

	// pending holds actions applied since the last tick was written. They are
	// attached to the next tick, which is where they belong: every action site
	// in the game runs with no Step between it and the following tick.
	pending []Action

	// The open run of consecutive ticks that share a target, coalesced until
	// the target changes, an action arrives, or a checkpoint falls due.
	open        bool
	x, y        float64
	n           int
	openActions []Action
}

// NewRecorder writes the header and returns a recorder. A nil sink returns a
// nil *Recorder and no error, which is how an absent -record flag is handled.
//
// The caller supplies Seed and the population targets; everything derivable is
// filled in here so no call site can forget it.
func NewRecorder(sink io.WriteCloser, h Header) (*Recorder, error) {
	if sink == nil {
		return nil, nil
	}

	h.Kind = KindHeader
	h.Format = Magic
	h.Version = Version
	h.TickRate = game.TickRate
	h.GOOS = runtime.GOOS
	h.GOARCH = runtime.GOARCH
	h.GoVersion = runtime.Version()
	if h.RecordedAt == "" {
		h.RecordedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if h.ChecksumEvery < 0 {
		h.ChecksumEvery = 0
	}

	r := &Recorder{
		enc:   json.NewEncoder(sink),
		w:     sink,
		every: h.ChecksumEvery,
	}
	if f, ok := sink.(interface{ Flush() error }); ok {
		r.flusher = f
	}
	if err := r.enc.Encode(h); err != nil {
		return nil, fmt.Errorf("writing replay header: %w", err)
	}
	return r, nil
}

// Action queues an action that has just been applied to the world. Call it
// immediately after the mutating call, so what is recorded is what actually
// reached the simulation — including no-ops, which are data about intent.
func (r *Recorder) Action(a Action) {
	if r == nil {
		return
	}
	r.pending = append(r.pending, a)
}

// Tick records the target for the tick about to be stepped, and attaches any
// queued actions to it.
//
// Pass the exact float64 pair given to SetPlayerTarget — compute ScreenToWorld
// once and hand the result to both. Computing it twice would agree only by
// accident.
func (r *Recorder) Tick(x, y float64) {
	if r == nil {
		return
	}
	// encoding/json cannot represent these, and a silent write failure would
	// corrupt the log at the one moment it matters.
	if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
		r.fail(fmt.Errorf("replay: refusing to record non-finite target (%v, %v)", x, y))
		return
	}

	// Extend the open run when the target is unchanged and nothing happened.
	if r.open && len(r.pending) == 0 && x == r.x && y == r.y {
		r.n++
		r.tick++
		return
	}

	// Otherwise close the open run and start a new one, which is what the
	// queued actions attach to: they were applied immediately before this tick.
	r.flushRun()
	r.open = true
	r.x, r.y = x, y
	r.n = 1
	r.openActions = r.takePending()
	r.tick++
}

// EndTick is called after Step. It emits a checkpoint on the configured
// cadence.
func (r *Recorder) EndTick(w *game.World) {
	if r == nil || r.every <= 0 || r.tick == 0 || r.tick%r.every != 0 {
		return
	}
	r.checkpoint(w, KindCheckpoint)
}

// Close writes the trailer and closes the sink. The trailer is how a reader
// knows the file is complete.
func (r *Recorder) Close(w *game.World) error {
	if r == nil {
		return nil
	}
	r.flushRun()

	// Actions applied after the final tick still changed the world the closing
	// checksum is taken from, so they have to be recorded or replay would
	// report a divergence that never happened. A run of zero ticks says
	// exactly that: applied, with no tick following.
	if len(r.pending) > 0 {
		r.write(Frame{
			Kind:    KindFrame,
			Tick:    r.tick,
			N:       0,
			X:       r.x,
			Y:       r.y,
			Actions: r.takePending(),
		})
	}

	r.checkpoint(w, KindEnd)
	err := r.err
	if cerr := r.w.Close(); err == nil {
		err = cerr
	}
	return err
}

// Ticks reports how many ticks have been recorded.
func (r *Recorder) Ticks() int {
	if r == nil {
		return 0
	}
	return r.tick
}

// Err reports the first write error. Failures are latched rather than returned
// per call, so the game loop stays free of error handling on a path that could
// do nothing useful with it.
func (r *Recorder) Err() error {
	if r == nil {
		return nil
	}
	return r.err
}

func (r *Recorder) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

// flushRun writes the open run of ticks, if any.
func (r *Recorder) flushRun() {
	if !r.open {
		return
	}
	f := Frame{
		Kind:    KindFrame,
		Tick:    r.tick - r.n,
		N:       r.n,
		X:       r.x,
		Y:       r.y,
		Actions: r.openActions,
	}
	r.open = false
	r.openActions = nil
	r.write(f)
}

// takePending hands ownership of the queued actions to the caller.
func (r *Recorder) takePending() []Action {
	if len(r.pending) == 0 {
		return nil
	}
	a := make([]Action, len(r.pending))
	copy(a, r.pending)
	r.pending = r.pending[:0]
	return a
}

func (r *Recorder) checkpoint(w *game.World, kind string) {
	// A checkpoint must not overtake the ticks it describes.
	r.flushRun()
	ck := Checkpoint{
		Kind:    kind,
		Tick:    r.tick,
		Hash:    fmt.Sprintf("%016x", Checksum(w)),
		Witness: Witnesses(w),
	}
	if kind == KindEnd {
		ck.Best = w.PlayerBest
	}
	r.write(ck)
}

func (r *Recorder) write(v any) {
	if r.err != nil {
		return
	}
	if err := r.enc.Encode(v); err != nil {
		r.fail(err)
		return
	}
	r.records++
	if r.flusher != nil && r.records%flushEvery == 0 {
		if err := r.flusher.Flush(); err != nil {
			r.fail(err)
		}
	}
}
