// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package agarienv

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"agario/internal/game"
)

// Server holds the live sessions. Several worlds are addressed by env_id, so a
// vectorised agent needs only one child process.
type Server struct {
	opt      Options
	sessions map[int]*session
}

// New returns a server with no sessions; the client creates them with reset.
func New(opt Options) *Server {
	return &Server{opt: opt, sessions: make(map[int]*session)}
}

// Run reads requests from in and writes one response per line to out. It
// returns when in reaches EOF or the client sends close, which is what lets the
// client stop the server simply by closing the pipe.
func (s *Server) Run(in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	// Batch requests are long; the default 64 KiB line limit is not enough.
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)

	w := bufio.NewWriter(out)
	enc := json.NewEncoder(w)

	for sc.Scan() {
		line := sc.Bytes()
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		resp, quit := s.Handle(line)
		if err := enc.Encode(resp); err != nil {
			return err
		}
		// Every response is flushed: the client is blocked reading one line and
		// will not send the next request until it arrives.
		if err := w.Flush(); err != nil {
			return err
		}
		if quit {
			return nil
		}
	}
	return sc.Err()
}

// Handle answers one request line. quit is true when the client asked to close.
func (s *Server) Handle(line []byte) (Response, bool) {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return Response{OK: false, Kind: ErrBadJSON, Error: err.Error()}, false
	}

	switch req.Cmd {
	case "hello":
		return Response{ID: req.ID, OK: true, Meta: s.meta()}, false
	case "reset":
		return s.reset(req), false
	case "step":
		return s.step(req), false
	case "state":
		return s.state(req), false
	case "reset_batch":
		return s.resetBatch(req), false
	case "step_batch":
		return s.stepBatch(req), false
	case "close":
		return Response{ID: req.ID, OK: true}, true
	default:
		return fail(req.ID, ErrUnknownCmd, fmt.Sprintf("unknown cmd %q", req.Cmd)), false
	}
}

func fail(id int, kind, msg string) Response {
	return Response{ID: id, OK: false, Kind: kind, Error: msg}
}

func (s *Server) meta() *Meta {
	return &Meta{
		Protocol:     Protocol,
		Game:         "agario",
		WorldSize:    game.WorldSize,
		TickDT:       game.TickDT,
		Frames:       s.opt.Frames,
		StepDT:       game.TickDT * float64(s.opt.Frames),
		MaxBlobs:     game.MaxBlobs,
		Headings:     Headings,
		Triggers:     NumTriggers,
		ViewScale:    s.opt.ViewScale,
		KFood:        s.opt.KFood,
		KCells:       s.opt.KCells,
		KVirus:       s.opt.KVirus,
		KEjecta:      s.opt.KEjecta,
		Food:         s.opt.Food,
		Bots:         s.opt.Bots,
		Viruses:      s.opt.Viruses,
		SplitMinMass: game.SplitMinMass,
		EjectMinMass: game.EjectMinMass,
	}
}

// seedOf returns the seed a reset should use. A client that omits one gets a
// deterministic function of the env id rather than a time-based seed, so a
// forgotten seed shows up as a suspiciously repeatable episode instead of
// silently destroying reproducibility.
func seedOf(req Request) int64 {
	if req.Seed != nil {
		return *req.Seed
	}
	return int64(req.EnvID) + 1
}

func (s *Server) reset(req Request) Response {
	sess := newSession(seedOf(req), s.opt, req.Config)
	s.sessions[req.EnvID] = sess
	st := sess.observe(req.EnvID)
	return Response{ID: req.ID, OK: true, State: &st}
}

func (s *Server) step(req Request) Response {
	sess, ok := s.sessions[req.EnvID]
	if !ok {
		return fail(req.ID, ErrNotReset, fmt.Sprintf("env %d has not been reset", req.EnvID))
	}
	if req.Action == nil {
		return fail(req.ID, ErrBadAction, "step needs an action")
	}
	if err := checkAction(*req.Action); err != nil {
		return fail(req.ID, ErrBadAction, err.Error())
	}
	sess.step(*req.Action)
	st := sess.observe(req.EnvID)
	return Response{ID: req.ID, OK: true, State: &st}
}

func (s *Server) state(req Request) Response {
	sess, ok := s.sessions[req.EnvID]
	if !ok {
		return fail(req.ID, ErrNotReset, fmt.Sprintf("env %d has not been reset", req.EnvID))
	}
	st := sess.observe(req.EnvID)
	return Response{ID: req.ID, OK: true, State: &st}
}

func checkAction(a [2]int) error {
	if a[0] < 0 || a[0] >= Headings {
		return fmt.Errorf("heading %d out of range [0,%d)", a[0], Headings)
	}
	if a[1] < 0 || a[1] >= NumTriggers {
		return fmt.Errorf("trigger %d out of range [0,%d)", a[1], NumTriggers)
	}
	return nil
}

func (s *Server) resetBatch(req Request) Response {
	n := len(req.EnvIDs)
	if n == 0 {
		return fail(req.ID, ErrBadBatch, "reset_batch needs env_ids")
	}
	if len(req.Seeds) != 0 && len(req.Seeds) != n {
		return fail(req.ID, ErrBadBatch, "seeds must be empty or match env_ids")
	}
	if len(req.Configs) != 0 && len(req.Configs) != n {
		return fail(req.ID, ErrBadBatch, "configs must be empty or match env_ids")
	}

	states := make([]State, n)
	for i, id := range req.EnvIDs {
		seed := int64(id) + 1
		if len(req.Seeds) == n {
			seed = req.Seeds[i]
		}
		var cfg *Config
		if len(req.Configs) == n {
			cfg = req.Configs[i]
		} else {
			cfg = req.Config
		}
		sess := newSession(seed, s.opt, cfg)
		s.sessions[id] = sess
		states[i] = sess.observe(id)
	}
	return Response{ID: req.ID, OK: true, States: states}
}

func (s *Server) stepBatch(req Request) Response {
	n := len(req.EnvIDs)
	if n == 0 {
		return fail(req.ID, ErrBadBatch, "step_batch needs env_ids")
	}
	if len(req.Actions) != n {
		return fail(req.ID, ErrBadBatch, "actions must match env_ids")
	}

	// Validate everything before mutating anything, so a bad batch leaves every
	// world untouched rather than half-stepped.
	for i, id := range req.EnvIDs {
		if _, ok := s.sessions[id]; !ok {
			return fail(req.ID, ErrNotReset, fmt.Sprintf("env %d has not been reset", id))
		}
		if err := checkAction(req.Actions[i]); err != nil {
			return fail(req.ID, ErrBadAction, fmt.Sprintf("env %d: %v", id, err))
		}
	}

	states := make([]State, n)
	for i, id := range req.EnvIDs {
		sess := s.sessions[id]
		sess.step(req.Actions[i])
		states[i] = sess.observe(id)
	}
	return Response{ID: req.ID, OK: true, States: states}
}
