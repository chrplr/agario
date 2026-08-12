// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package agarienv

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"agario/internal/game"
)

func newTestServer() *Server {
	opt := DefaultOptions()
	// Small populations keep the tests quick and the JSON readable.
	opt.Food, opt.Bots, opt.Viruses = 60, 3, 3
	return New(opt)
}

// ask sends one request and returns the decoded response.
func ask(t *testing.T, s *Server, req string) Response {
	t.Helper()
	resp, _ := s.Handle([]byte(req))
	return resp
}

func mustOK(t *testing.T, resp Response) Response {
	t.Helper()
	if !resp.OK {
		t.Fatalf("request failed: kind=%q error=%q", resp.Kind, resp.Error)
	}
	return resp
}

func TestHandshakeDescribesTheSpaces(t *testing.T) {
	s := newTestServer()
	resp := mustOK(t, ask(t, s, `{"id":1,"cmd":"hello"}`))

	m := resp.Meta
	if m == nil {
		t.Fatal("hello returned no meta")
	}
	if m.Protocol != Protocol {
		t.Errorf("protocol = %d, want %d", m.Protocol, Protocol)
	}
	// The client builds its spaces from these, so a zero here is a silent
	// shape bug on the Python side.
	for name, v := range map[string]int{
		"frames": m.Frames, "max_blobs": m.MaxBlobs, "headings": m.Headings,
		"triggers": m.Triggers, "k_food": m.KFood, "k_cells": m.KCells,
		"k_virus": m.KVirus, "k_ejecta": m.KEjecta,
	} {
		if v <= 0 {
			t.Errorf("meta.%s = %d, want positive", name, v)
		}
	}
	if m.WorldSize != game.WorldSize {
		t.Errorf("world_size = %v, want %v", m.WorldSize, game.WorldSize)
	}
	if want := game.TickDT * float64(m.Frames); m.StepDT != want {
		t.Errorf("step_dt = %v, want %v", m.StepDT, want)
	}
}

func TestResetThenStep(t *testing.T) {
	s := newTestServer()
	resp := mustOK(t, ask(t, s, `{"id":1,"cmd":"reset","env_id":0,"seed":42}`))
	st := resp.State
	if st == nil {
		t.Fatal("reset returned no state")
	}
	if st.Mass != game.PlayerStartMass {
		t.Errorf("starting mass = %v, want %v", st.Mass, game.PlayerStartMass)
	}
	if st.SelfCount != 1 {
		t.Errorf("starting cells = %d, want 1", st.SelfCount)
	}
	if st.Dead {
		t.Error("agent is dead at reset")
	}

	resp = mustOK(t, ask(t, s, `{"id":2,"cmd":"step","env_id":0,"action":[0,0]}`))
	if resp.State.Tick != 1 {
		t.Errorf("tick after one step = %d, want 1", resp.State.Tick)
	}
	if resp.State.Time <= st.Time {
		t.Error("world time did not advance")
	}
}

// Padding is the contract that keeps the observation shape independent of how
// many entities happen to be nearby.
func TestArraysArePaddedToTheHandshakeLengths(t *testing.T) {
	s := newTestServer()
	m := mustOK(t, ask(t, s, `{"id":1,"cmd":"hello"}`)).Meta
	st := mustOK(t, ask(t, s, `{"id":2,"cmd":"reset","env_id":0,"seed":1}`)).State

	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"food", len(st.Food), m.KFood},
		{"cells", len(st.Cells), m.KCells},
		{"viruses", len(st.Viruses), m.KVirus},
		{"ejecta", len(st.Ejecta), m.KEjecta},
		{"self", len(st.Self), m.MaxBlobs},
	} {
		if c.got != c.want {
			t.Errorf("len(%s) = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestNeighboursAreSortedByDistance(t *testing.T) {
	s := newTestServer()
	st := mustOK(t, ask(t, s, `{"id":1,"cmd":"reset","env_id":0,"seed":3}`)).State

	prev := -1.0
	for _, f := range st.Food {
		if f[2] == 0 {
			break // padding
		}
		d := f[0]*f[0] + f[1]*f[1]
		if d < prev {
			t.Fatalf("food not sorted by distance: %v after %v", d, prev)
		}
		prev = d
	}
}

func TestSameSeedGivesTheSameTrajectory(t *testing.T) {
	run := func() []float64 {
		s := newTestServer()
		mustOK(t, ask(t, s, `{"id":1,"cmd":"reset","env_id":0,"seed":99}`))
		var masses []float64
		for i := 0; i < 40; i++ {
			r := mustOK(t, ask(t, s, `{"id":2,"cmd":"step","env_id":0,"action":[3,0]}`))
			masses = append(masses, r.State.Mass, r.State.CX, r.State.CY)
		}
		return masses
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same seed diverged at %d: %v vs %v", i, a[i], b[i])
		}
	}

	// And a different seed must actually differ, or the seed is being ignored.
	other := func() []float64 {
		s := newTestServer()
		mustOK(t, ask(t, s, `{"id":1,"cmd":"reset","env_id":0,"seed":1234}`))
		var out []float64
		for i := 0; i < 40; i++ {
			r := mustOK(t, ask(t, s, `{"id":2,"cmd":"step","env_id":0,"action":[3,0]}`))
			out = append(out, r.State.Mass, r.State.CX, r.State.CY)
		}
		return out
	}()
	same := true
	for i := range a {
		if a[i] != other[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different seeds produced identical trajectories — the seed is ignored")
	}
}

func TestErrorsAreTaggedAndNonFatal(t *testing.T) {
	s := newTestServer()

	for _, c := range []struct {
		name string
		req  string
		kind string
	}{
		{"not json", `{oops`, ErrBadJSON},
		{"unknown cmd", `{"id":1,"cmd":"fly"}`, ErrUnknownCmd},
		{"step before reset", `{"id":2,"cmd":"step","env_id":7,"action":[0,0]}`, ErrNotReset},
		{"state before reset", `{"id":3,"cmd":"state","env_id":7}`, ErrNotReset},
	} {
		resp := ask(t, s, c.req)
		if resp.OK {
			t.Errorf("%s: expected failure", c.name)
		}
		if resp.Kind != c.kind {
			t.Errorf("%s: kind = %q, want %q", c.name, resp.Kind, c.kind)
		}
	}

	// After all that the server must still work: a failed command is not fatal.
	mustOK(t, ask(t, s, `{"id":9,"cmd":"reset","env_id":0,"seed":1}`))
}

func TestBadActionsAreRejected(t *testing.T) {
	s := newTestServer()
	mustOK(t, ask(t, s, `{"id":1,"cmd":"reset","env_id":0,"seed":1}`))

	for _, req := range []string{
		`{"id":2,"cmd":"step","env_id":0,"action":[16,0]}`,
		`{"id":3,"cmd":"step","env_id":0,"action":[-1,0]}`,
		`{"id":4,"cmd":"step","env_id":0,"action":[0,3]}`,
		`{"id":5,"cmd":"step","env_id":0,"action":[0,-1]}`,
	} {
		resp := ask(t, s, req)
		if resp.OK || resp.Kind != ErrBadAction {
			t.Errorf("%s: expected bad_action, got ok=%v kind=%q", req, resp.OK, resp.Kind)
		}
	}
	// A step with no action at all.
	if resp := ask(t, s, `{"id":6,"cmd":"step","env_id":0}`); resp.OK || resp.Kind != ErrBadAction {
		t.Errorf("missing action: got ok=%v kind=%q", resp.OK, resp.Kind)
	}
}

func TestTriggersDoWhatTheySay(t *testing.T) {
	s := newTestServer()
	mustOK(t, ask(t, s, `{"id":1,"cmd":"reset","env_id":0,"seed":5}`))
	sess := s.sessions[0]

	// A starting cell is below both thresholds, so the flags must say no and the
	// triggers must be no-ops rather than errors.
	st := sess.observe(0)
	if st.CanSplit || st.CanEject {
		t.Fatalf("a %v-mass cell should not be able to split or eject", st.Mass)
	}
	mustOK(t, ask(t, s, `{"id":2,"cmd":"step","env_id":0,"action":[0,1]}`))
	if got := len(sess.world.Player.Blobs); got != 1 {
		t.Errorf("split below the minimum produced %d cells, want 1", got)
	}

	// Feed it past the thresholds and try again.
	sess.world.Player.Blobs[0].Mass = 400
	st = sess.observe(0)
	if !st.CanSplit || !st.CanEject {
		t.Fatalf("a 400-mass cell should be able to split and eject, got %v/%v", st.CanSplit, st.CanEject)
	}
	mustOK(t, ask(t, s, `{"id":3,"cmd":"step","env_id":0,"action":[0,1]}`))
	if got := len(sess.world.Player.Blobs); got != 2 {
		t.Errorf("split produced %d cells, want 2", got)
	}

	before := len(sess.world.Ejectas)
	mustOK(t, ask(t, s, `{"id":4,"cmd":"step","env_id":0,"action":[0,2]}`))
	if len(sess.world.Ejectas) <= before {
		t.Error("eject produced no pellet")
	}
}

func TestConfigOverridesPopulations(t *testing.T) {
	s := newTestServer()
	st := mustOK(t, ask(t, s,
		`{"id":1,"cmd":"reset","env_id":0,"seed":1,"config":{"food":10,"bots":1,"viruses":0}}`)).State
	if st == nil {
		t.Fatal("no state")
	}
	sess := s.sessions[0]
	if got := len(sess.world.Food); got != 10 {
		t.Errorf("food = %d, want 10", got)
	}
	if got := len(sess.world.Viruses); got != 0 {
		t.Errorf("viruses = %d, want 0", got)
	}
	bots := 0
	for _, o := range sess.world.Owners {
		if o.IsBot {
			bots++
		}
	}
	if bots != 1 {
		t.Errorf("bots = %d, want 1", bots)
	}
}

func TestBatchMatchesIndividualSteps(t *testing.T) {
	// Two worlds stepped through the batch commands must match the same two
	// worlds stepped one at a time; this is what the vector env relies on.
	batch := newTestServer()
	mustOK(t, ask(t, batch, `{"id":1,"cmd":"reset_batch","env_ids":[0,1],"seeds":[11,22]}`))
	var batched []float64
	for i := 0; i < 20; i++ {
		r := mustOK(t, ask(t, batch, `{"id":2,"cmd":"step_batch","env_ids":[0,1],"actions":[[2,0],[9,0]]}`))
		if len(r.States) != 2 {
			t.Fatalf("step_batch returned %d states, want 2", len(r.States))
		}
		batched = append(batched, r.States[0].Mass, r.States[0].CX, r.States[1].Mass, r.States[1].CX)
	}

	single := newTestServer()
	mustOK(t, ask(t, single, `{"id":1,"cmd":"reset","env_id":0,"seed":11}`))
	mustOK(t, ask(t, single, `{"id":2,"cmd":"reset","env_id":1,"seed":22}`))
	var one []float64
	for i := 0; i < 20; i++ {
		a := mustOK(t, ask(t, single, `{"id":3,"cmd":"step","env_id":0,"action":[2,0]}`))
		b := mustOK(t, ask(t, single, `{"id":4,"cmd":"step","env_id":1,"action":[9,0]}`))
		one = append(one, a.State.Mass, a.State.CX, b.State.Mass, b.State.CX)
	}

	for i := range batched {
		if batched[i] != one[i] {
			t.Fatalf("batch diverged from individual stepping at %d: %v vs %v", i, batched[i], one[i])
		}
	}
}

func TestBadBatchLeavesEveryWorldUntouched(t *testing.T) {
	s := newTestServer()
	mustOK(t, ask(t, s, `{"id":1,"cmd":"reset_batch","env_ids":[0,1],"seeds":[1,2]}`))
	before := s.sessions[0].observe(0).Time

	// The second action is invalid; neither world may advance.
	resp := ask(t, s, `{"id":2,"cmd":"step_batch","env_ids":[0,1],"actions":[[0,0],[99,0]]}`)
	if resp.OK || resp.Kind != ErrBadAction {
		t.Fatalf("expected bad_action, got ok=%v kind=%q", resp.OK, resp.Kind)
	}
	if after := s.sessions[0].observe(0).Time; after != before {
		t.Errorf("world 0 advanced despite a rejected batch: %v -> %v", before, after)
	}

	for _, req := range []string{
		`{"id":3,"cmd":"step_batch","env_ids":[],"actions":[]}`,
		`{"id":4,"cmd":"step_batch","env_ids":[0,1],"actions":[[0,0]]}`,
		`{"id":5,"cmd":"reset_batch","env_ids":[0,1],"seeds":[1]}`,
	} {
		if resp := ask(t, s, req); resp.OK {
			t.Errorf("%s: expected failure", req)
		}
	}
}

func TestDeadAgentStillProducesAWellFormedState(t *testing.T) {
	s := newTestServer()
	mustOK(t, ask(t, s, `{"id":1,"cmd":"reset","env_id":0,"seed":1}`))
	sess := s.sessions[0]

	// Kill the agent outright, the way being eaten would.
	sess.world.Player.Blobs = nil
	sess.world.Player.Dead = true

	st := sess.observe(0)
	if !st.Dead {
		t.Error("state does not report death")
	}
	if len(st.Food) != s.opt.KFood || len(st.Self) != game.MaxBlobs {
		t.Error("padding is wrong for a dead agent, so the observation shape changes")
	}
	if st.CanSplit || st.CanEject {
		t.Error("a dead agent should not be able to act")
	}
	// It must also survive being marshalled — NaN would make encoding/json fail.
	if _, err := json.Marshal(st); err != nil {
		t.Errorf("dead state does not marshal: %v", err)
	}
}

func TestRunAnswersEveryLineAndStopsOnClose(t *testing.T) {
	in := strings.NewReader(
		`{"id":1,"cmd":"hello"}` + "\n" +
			"\n" + // blank lines are skipped, not answered
			`{"id":2,"cmd":"reset","env_id":0,"seed":1}` + "\n" +
			`{"id":3,"cmd":"close"}` + "\n" +
			`{"id":4,"cmd":"hello"}` + "\n") // after close: never read

	var out bytes.Buffer
	if err := newTestServer().Run(in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d responses, want 3: %q", len(lines), out.String())
	}
	for i, want := range []int{1, 2, 3} {
		var r Response
		if err := json.Unmarshal([]byte(lines[i]), &r); err != nil {
			t.Fatalf("response %d is not JSON: %v", i, err)
		}
		if r.ID != want {
			t.Errorf("response %d has id %d, want %d", i, r.ID, want)
		}
		if !r.OK {
			t.Errorf("response %d failed: %s", i, r.Error)
		}
	}
}

func TestRunStopsAtEOF(t *testing.T) {
	// Closing the client's pipe must end the server even without a close
	// command; it is the backstop that stops orphaned processes.
	var out bytes.Buffer
	if err := newTestServer().Run(strings.NewReader(`{"id":1,"cmd":"hello"}`+"\n"), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), `"ok":true`) {
		t.Errorf("no answer before EOF: %q", out.String())
	}
}

func BenchmarkStepCommand(b *testing.B) {
	s := newTestServer()
	s.Handle([]byte(`{"id":1,"cmd":"reset","env_id":0,"seed":1}`))
	req := []byte(`{"id":2,"cmd":"step","env_id":0,"action":[3,0]}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Handle(req)
	}
}
