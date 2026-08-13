// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package replay

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"

	"agario/internal/game"
)

// Player replays a recorded session into a world.
//
// The whole log is decoded up front — a recorded hour is a few million bytes of
// frames — which is what makes seeking possible on a format that has no random
// access.
type Player struct {
	hdr    Header
	frames []Frame
	checks map[int]Checkpoint
	end    *Checkpoint
	total  int

	// truncated records that the log had no end marker: the process that wrote
	// it was killed, so the tail is missing but everything before it is sound.
	truncated bool

	tick int
	fi   int // frames index covering tick
	done bool
	divs []Divergence
}

// Divergence is a checkpoint that did not match. Want is what was recorded,
// Got is what this replay produced.
type Divergence struct {
	Tick      int
	Want, Got string
	WantW     Witness
	GotW      Witness
}

// String renders a divergence as a one-line summary plus the witnesses that
// differ, which is what turns "it desynced" into "a virus pop went missing".
func (d Divergence) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "tick %d: recorded %s, replayed %s", d.Tick, d.Want, d.Got)
	for _, f := range []struct {
		name      string
		want, got any
	}{
		{"time", d.WantW.Time, d.GotW.Time},
		{"mass", d.WantW.Mass, d.GotW.Mass},
		{"blobs", d.WantW.Blobs, d.GotW.Blobs},
		{"food", d.WantW.Food, d.GotW.Food},
		{"ejecta", d.WantW.Ejecta, d.GotW.Ejecta},
		{"virus", d.WantW.Virus, d.GotW.Virus},
		{"deaths", d.WantW.Deaths, d.GotW.Deaths},
	} {
		if f.want != f.got {
			fmt.Fprintf(&b, "\n    %-7s recorded %v, replayed %v", f.name, f.want, f.got)
		}
	}
	return b.String()
}

// Open reads a recording, transparently decompressing a gzipped one.
func Open(r io.Reader) (*Player, error) {
	br := bufio.NewReader(r)
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("reading gzipped replay: %w", err)
		}
		defer gz.Close()
		return parse(bufio.NewReader(gz))
	}
	return parse(br)
}

func parse(br *bufio.Reader) (*Player, error) {
	p := &Player{checks: map[int]Checkpoint{}}

	sc := bufio.NewScanner(br)
	// Frames are small, but a header with a long version string plus room to
	// grow costs nothing to allow.
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)

	first := true
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}

		var probe struct {
			Kind string `json:"k"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			// A partial final line is truncation, not corruption: the writer
			// was killed mid-record. Everything before it stands.
			p.truncated = true
			break
		}

		if first {
			if probe.Kind != KindHeader {
				return nil, errors.New("not a replay file: first line is not a header")
			}
			if err := json.Unmarshal(line, &p.hdr); err != nil {
				return nil, fmt.Errorf("reading replay header: %w", err)
			}
			if p.hdr.Format != Magic {
				return nil, fmt.Errorf("not a replay file: format is %q, want %q", p.hdr.Format, Magic)
			}
			if p.hdr.Version != Version {
				return nil, fmt.Errorf("replay file is version %d, this build reads version %d",
					p.hdr.Version, Version)
			}
			first = false
			continue
		}

		switch probe.Kind {
		case KindFrame:
			var f Frame
			if err := json.Unmarshal(line, &f); err != nil {
				p.truncated = true
				continue
			}
			p.frames = append(p.frames, f)
			p.total += f.N
		case KindCheckpoint, KindEnd:
			var ck Checkpoint
			if err := json.Unmarshal(line, &ck); err != nil {
				p.truncated = true
				continue
			}
			if probe.Kind == KindEnd {
				p.end = &ck
			} else {
				p.checks[ck.Tick] = ck
			}
		}
	}
	if err := sc.Err(); err != nil {
		// A too-long line or an I/O error mid-file: keep the prefix.
		p.truncated = true
	}
	if first {
		return nil, errors.New("empty replay file")
	}
	if p.end == nil {
		p.truncated = true
	}

	// Sequential replay relies on frames being in tick order. They are as
	// written, but a hand-edited file should not silently desync.
	sort.SliceStable(p.frames, func(i, j int) bool { return p.frames[i].Tick < p.frames[j].Tick })
	return p, nil
}

// Header returns the recording's header.
func (p *Player) Header() Header { return p.hdr }

// Ticks is the number of ticks in the recording.
func (p *Player) Ticks() int { return p.total }

// Tick is the current position.
func (p *Player) Tick() int { return p.tick }

// Truncated reports that the recording has no end marker, so it stops early.
func (p *Player) Truncated() bool { return p.truncated }

// Done reports that the whole recording has been replayed.
func (p *Player) Done() bool { return p.done }

// Divergences returns every checkpoint that did not match, in tick order.
func (p *Player) Divergences() []Divergence { return p.divs }

// Provenance returns a warning when the recording was made somewhere bit-exact
// replay is not guaranteed, and "" otherwise. Reporting this before a checksum
// mismatch is what stops an expected floating-point difference from looking
// like a bug in the replay itself.
func (p *Player) Provenance() string {
	var diffs []string
	if p.hdr.GOOS != runtime.GOOS || p.hdr.GOARCH != runtime.GOARCH {
		diffs = append(diffs, fmt.Sprintf("recorded on %s/%s, replaying on %s/%s",
			p.hdr.GOOS, p.hdr.GOARCH, runtime.GOOS, runtime.GOARCH))
	}
	if p.hdr.GoVersion != runtime.Version() {
		diffs = append(diffs, fmt.Sprintf("recorded with %s, replaying with %s",
			p.hdr.GoVersion, runtime.Version()))
	}
	if len(diffs) == 0 {
		return ""
	}
	return strings.Join(diffs, "; ") +
		" — math.Exp and math.Pow differ across architectures and Go versions, so bit-exact replay is not guaranteed"
}

// NewWorld builds the world this recording started from.
func (p *Player) NewWorld() *game.World {
	w := game.NewWorld(p.hdr.Seed)
	if p.hdr.Food != 0 || p.hdr.Bots != 0 || p.hdr.Viruses != 0 {
		w.FoodTarget, w.BotTarget, w.VirusTarget = p.hdr.Food, p.hdr.Bots, p.hdr.Viruses
	}
	return w
}

// Advance applies exactly one tick and reports whether it did.
//
// The order below is the contract this package exists to keep: the actions
// recorded against this tick, then the target, then Step, then the checkpoint
// comparison. It is the order the live game runs in, and any other order
// desynchronises.
func (p *Player) Advance(w *game.World) bool {
	if p.tick >= p.total {
		p.finish(w)
		return false
	}

	// Find the run covering this tick. Zero-tick runs carry trailing actions
	// and are handled by finish, so they are skipped here.
	for p.fi < len(p.frames) && (p.frames[p.fi].N == 0 ||
		p.tick >= p.frames[p.fi].Tick+p.frames[p.fi].N) {
		p.fi++
	}
	if p.fi >= len(p.frames) {
		p.finish(w)
		return false
	}
	f := p.frames[p.fi]

	if p.tick == f.Tick {
		applyActions(w, f.Actions)
	}
	w.SetPlayerTarget(f.X, f.Y) // a no-op under autopilot, exactly as it was live
	w.Step(game.TickDT)
	p.tick++

	if ck, ok := p.checks[p.tick]; ok {
		p.compare(w, ck)
	}
	return true
}

// finish applies any actions recorded after the final tick and compares the
// closing checkpoint. Trailing actions matter because they changed the world
// the recorded checksum was taken from.
func (p *Player) finish(w *game.World) {
	if p.done {
		return
	}
	p.done = true
	for _, f := range p.frames {
		if f.N == 0 && f.Tick == p.total {
			applyActions(w, f.Actions)
		}
	}
	if p.end != nil {
		p.compare(w, *p.end)
	}
}

func (p *Player) compare(w *game.World, ck Checkpoint) {
	got := fmt.Sprintf("%016x", Checksum(w))
	if got == ck.Hash {
		return
	}
	p.divs = append(p.divs, Divergence{
		Tick:  ck.Tick,
		Want:  ck.Hash,
		Got:   got,
		WantW: ck.Witness,
		GotW:  Witnesses(w),
	})
}

// Seek returns a world positioned at tick n.
//
// Forward it simply advances. Backward it rebuilds from the seed and
// re-simulates, because math/rand's generator cannot be serialised, so there is
// no snapshot to jump to. At roughly 18.5 µs a tick, rewinding ten minutes of
// game time costs about 1.3 s.
func (p *Player) Seek(w *game.World, n int) *game.World {
	if n < 0 {
		n = 0
	}
	if n > p.total {
		n = p.total
	}
	if n < p.tick {
		w = p.NewWorld()
		p.tick, p.fi, p.done = 0, 0, false
		// Divergences found on the way past are still real; keep them, but do
		// not report them twice when the same ticks are replayed again.
		p.divs = nil
	}
	for p.tick < n {
		if !p.Advance(w) {
			break
		}
	}
	return w
}

func applyActions(w *game.World, actions []Action) {
	for _, a := range actions {
		switch a {
		case ActSplit:
			w.Split(w.Player)
		case ActEject:
			w.Eject(w.Player)
		case ActRespawn:
			w.RespawnPlayer()
		case ActAutopilotOn:
			w.SetAutopilot(true)
		case ActAutopilotOff:
			w.SetAutopilot(false)
		}
	}
}
