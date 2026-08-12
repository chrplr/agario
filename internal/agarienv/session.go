// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package agarienv

import (
	"math"
	"sort"

	"agario/internal/game"
)

// Headings is the number of discrete compass directions an action can steer in.
// Sixteen is ample: a cell turns slowly enough that finer aiming is wasted, and
// it keeps the action space small enough for any of the usual algorithms.
const Headings = 16

// Triggers: do nothing, split, or eject mass.
const (
	TriggerNone = iota
	TriggerSplit
	TriggerEject
	NumTriggers
)

// session is one world plus the bookkeeping an episode needs.
type session struct {
	world *game.World
	opt   Options

	steps int

	// Filled by step, read by observe: the deltas for the step just taken.
	massGain float64
	kills    int
}

// Options are the server-wide knobs, fixed at startup and reported in the
// handshake.
type Options struct {
	// Frames is how many simulation ticks one step command advances. The
	// simulation runs at 120 Hz, so stepping one tick per decision would make an
	// episode hundreds of thousands of steps long; 4 gives 30 decisions/second.
	Frames int

	// K* cap how many neighbours of each kind are reported. Arrays are padded to
	// exactly these lengths.
	KFood   int
	KCells  int
	KVirus  int
	KEjecta int

	// ViewScale sets the cull radius as a multiple of the agent's own radius.
	// It mirrors what the camera shows a human player, so the agent is not
	// handed information the game never displays.
	ViewScale float64

	Food    int
	Bots    int
	Viruses int
}

// DefaultOptions are used when a flag is not given.
func DefaultOptions() Options {
	return Options{
		Frames:    4,
		KFood:     32,
		KCells:    16,
		KVirus:    8,
		KEjecta:   8,
		ViewScale: 12,
		Food:      game.MaxFood,
		Bots:      game.MaxBots,
		Viruses:   game.MaxViruses,
	}
}

// newSession builds a world from a seed, applying any per-episode overrides.
func newSession(seed int64, opt Options, cfg *Config) *session {
	w := game.NewWorld(seed)

	w.FoodTarget = opt.Food
	w.BotTarget = opt.Bots
	w.VirusTarget = opt.Viruses
	if cfg != nil {
		if cfg.Food != nil {
			w.FoodTarget = *cfg.Food
		}
		if cfg.Bots != nil {
			w.BotTarget = *cfg.Bots
		}
		if cfg.Viruses != nil {
			w.VirusTarget = *cfg.Viruses
		}
	}
	// The world was built with the compile-time populations; trim any surplus so
	// a request for a small arena takes effect immediately rather than decaying
	// towards the target over the first minute.
	if len(w.Food) > w.FoodTarget {
		w.Food = w.Food[:w.FoodTarget]
	}
	if len(w.Viruses) > w.VirusTarget {
		w.Viruses = w.Viruses[:w.VirusTarget]
	}
	trimBots(w)

	return &session{world: w, opt: opt}
}

// trimBots drops surplus bot owners so BotTarget applies from the first tick.
func trimBots(w *game.World) {
	kept := w.Owners[:0]
	bots := 0
	for _, o := range w.Owners {
		if o.IsBot {
			if bots >= w.BotTarget {
				continue
			}
			bots++
		}
		kept = append(kept, o)
	}
	w.Owners = kept
}

// step applies one action and advances the world by Frames ticks.
func (s *session) step(action [2]int) {
	heading, trigger := action[0], action[1]
	p := s.world.Player

	massBefore := p.Mass()
	watched := s.enemyBlobsInView()

	// Aim at a point well beyond the arena along the chosen bearing, so the
	// cells commit to the heading rather than easing to a stop on a nearby
	// target. stepBlob ramps speed down only inside the blob's own radius.
	angle := float64(heading) * 2 * math.Pi / Headings
	cx, cy := p.Center()
	s.world.SetPlayerTarget(
		cx+math.Cos(angle)*game.WorldSize,
		cy+math.Sin(angle)*game.WorldSize,
	)

	if !p.Dead {
		switch trigger {
		case TriggerSplit:
			s.world.Split(p)
		case TriggerEject:
			s.world.Eject(p)
		}
	}

	for i := 0; i < s.opt.Frames; i++ {
		s.world.Step(game.TickDT)
		if s.world.Player.Dead {
			// Stop on death rather than simulating the rest of the frame skip:
			// the episode is over and the remaining ticks cannot change it.
			break
		}
	}
	s.steps++

	s.massGain = p.Mass() - massBefore
	s.kills = s.countVanished(watched)
}

// enemyBlobsInView records the ids of enemy cells near enough to be plausible
// prey, so countVanished can tell afterwards which of them disappeared.
func (s *session) enemyBlobsInView() map[uint32]bool {
	cx, cy := s.world.Player.Center()
	view := s.opt.ViewScale * game.MassToRadius(math.Max(s.world.Player.Mass(), game.PlayerStartMass))
	v2 := view * view

	watched := make(map[uint32]bool)
	for _, o := range s.world.Owners {
		if o == s.world.Player {
			continue
		}
		for _, b := range o.Blobs {
			dx, dy := b.X-cx, b.Y-cy
			if dx*dx+dy*dy <= v2 {
				watched[b.ID] = true
			}
		}
	}
	return watched
}

// countVanished reports how many watched cells no longer exist. See the comment
// on State.Kills: this is a proxy, and it only counts when the agent actually
// gained mass, which rules out the common case of watching two bots fight.
func (s *session) countVanished(watched map[uint32]bool) int {
	if len(watched) == 0 || s.massGain <= 0 {
		return 0
	}
	alive := make(map[uint32]bool, len(watched))
	for _, o := range s.world.Owners {
		for _, b := range o.Blobs {
			if watched[b.ID] {
				alive[b.ID] = true
			}
		}
	}
	return len(watched) - len(alive)
}

// observe builds the wire state. It is a pure read of the world.
func (s *session) observe(envID int) State {
	w := s.world
	p := w.Player

	st := State{
		EnvID:     envID,
		Tick:      s.steps,
		Time:      w.Time,
		Dead:      p.Dead,
		SelfCount: len(p.Blobs),
		MassGain:  s.massGain,
		Kills:     s.kills,
	}

	cx, cy := p.Center()
	st.CX, st.CY = cx, cy
	st.Mass = p.Mass()
	st.Radius = game.MassToRadius(st.Mass)

	// A dead agent still needs a well-formed observation: the client reads it
	// once more before the episode ends.
	biggest := 0.0
	if b := p.LargestBlob(); b != nil {
		biggest = b.Mass
	}

	st.ViewR = math.Max(game.MassToRadius(math.Max(biggest, game.PlayerStartMass))*s.opt.ViewScale, 600)
	view := st.ViewR

	st.WallLeft = cx / game.WorldSize
	st.WallRight = (game.WorldSize - cx) / game.WorldSize
	st.WallUp = cy / game.WorldSize
	st.WallDown = (game.WorldSize - cy) / game.WorldSize

	st.CanSplit = !p.Dead && len(p.Blobs) < game.MaxBlobs && biggest >= game.SplitMinMass
	st.CanEject = !p.Dead && biggest >= game.EjectMinMass

	// Own cells, padded. Position is relative to the centroid so the encoding is
	// translation invariant.
	st.Self = make([][4]float64, game.MaxBlobs)
	for i, b := range p.Blobs {
		if i >= game.MaxBlobs {
			break
		}
		wait := b.MergeAt - w.Time
		if wait < 0 {
			wait = 0
		}
		st.Self[i] = [4]float64{b.X - cx, b.Y - cy, b.Mass, wait}
	}

	st.Food = nearest3(w.Food, s.opt.KFood, cx, cy, view,
		func(f *game.Food) (float64, float64, float64) { return f.X, f.Y, f.Mass })
	st.Ejecta = nearest3(w.Ejectas, s.opt.KEjecta, cx, cy, view,
		func(e *game.Ejecta) (float64, float64, float64) { return e.X, e.Y, e.Mass })
	st.Viruses = nearest3(w.Viruses, s.opt.KVirus, cx, cy, view,
		func(v *game.Virus) (float64, float64, float64) { return v.X, v.Y, v.Mass })

	st.Cells = s.nearestCells(cx, cy, view, biggest)

	// Ranking, so the agent has a notion of how it is doing overall.
	for _, o := range w.Owners {
		if !o.Dead && len(o.Blobs) > 0 {
			st.NOwners++
		}
	}
	if board := w.Leaderboard(len(w.Owners)); len(board) > 0 {
		st.TopMass = board[0].Mass
		st.Rank = len(board)
		for i, e := range board {
			if e.IsPlayer {
				st.Rank = i + 1
				break
			}
		}
	}

	return st
}

// nearest3 collects the K nearest entities within view, as {dx, dy, mass},
// padded with zeros. The generic keeps one implementation for food, ejecta and
// viruses.
func nearest3[T any](items []T, k int, cx, cy, view float64, pos func(T) (float64, float64, float64)) [][3]float64 {
	type cand struct {
		d2         float64
		dx, dy, ms float64
	}
	found := make([]cand, 0, k+1)
	v2 := view * view

	for _, it := range items {
		x, y, m := pos(it)
		dx, dy := x-cx, y-cy
		d2 := dx*dx + dy*dy
		if d2 > v2 {
			continue
		}
		found = append(found, cand{d2, dx, dy, m})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].d2 < found[j].d2 })

	out := make([][3]float64, k)
	for i := 0; i < k && i < len(found); i++ {
		out[i] = [3]float64{found[i].dx, found[i].dy, found[i].ms}
	}
	return out
}

// nearestCells collects the K nearest enemy blobs, tagged with whether they are
// a threat or prey. The relation is computed with the game's own CanEat rule so
// the client never has to re-derive the 1.25 mass ratio.
func (s *session) nearestCells(cx, cy, view, biggest float64) [][4]float64 {
	type cand struct {
		d2              float64
		dx, dy, ms, rel float64
	}
	var found []cand
	v2 := view * view
	me := s.world.Player

	for _, o := range s.world.Owners {
		if o == me {
			continue
		}
		for _, b := range o.Blobs {
			dx, dy := b.X-cx, b.Y-cy
			d2 := dx*dx + dy*dy
			if d2 > v2 {
				continue
			}
			rel := RelationNeutral
			// Threat if it could eat any of the agent's cells at contact;
			// prey if the agent's largest could eat it.
			for _, mine := range me.Blobs {
				if game.CanEat(b.Mass, mine.Mass, 0) {
					rel = RelationThreat
					break
				}
			}
			if rel == RelationNeutral && biggest > 0 && game.CanEat(biggest, b.Mass, 0) {
				rel = RelationPrey
			}
			found = append(found, cand{d2, dx, dy, b.Mass, rel})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].d2 < found[j].d2 })

	out := make([][4]float64, s.opt.KCells)
	for i := 0; i < s.opt.KCells && i < len(found); i++ {
		out[i] = [4]float64{found[i].dx, found[i].dy, found[i].ms, found[i].rel}
	}
	return out
}
