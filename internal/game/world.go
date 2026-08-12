// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package game

import (
	"fmt"
	"math"
	"math/rand"
)

// World holds all simulation state. It imports nothing SDL-related, so it can
// be stepped headlessly in tests and benchmarks.
type World struct {
	Rng *rand.Rand

	// Time is the world clock in seconds; merge cooldowns are absolute times
	// against it.
	Time float64

	Player *Owner
	Owners []*Owner

	// Population targets that maintain() keeps topped up. They default to the
	// constants in config.go; setting one to zero freezes that population,
	// which is how tests isolate a scenario from background respawns.
	FoodTarget  int
	BotTarget   int
	VirusTarget int

	Food    []*Food
	Ejectas []*Ejecta
	Viruses []*Virus

	// Broad-phase grids, rebuilt at the start of each tick.
	foodGrid   *Grid[int]
	ejectaGrid *Grid[int]
	blobGrid   *Grid[*Blob]

	nextOwnerID uint32
	nextBlobID  uint32

	// Stats for the HUD.
	PlayerDeaths int
	// Highest total mass the player has reached this life.
	PlayerBest float64

	// scratch reused across ticks to avoid per-tick allocation.
	eatenFood   []int
	eatenEjecta []int
}

var foodColors = []Color{
	{233, 78, 78}, {243, 156, 18}, {241, 196, 15},
	{46, 204, 113}, {52, 152, 219}, {155, 89, 182}, {26, 188, 156},
}

var botNames = []string{
	"Nibbler", "Vortex", "Peppermint", "Zephyr", "Bramble", "Quasar",
	"Marmalade", "Tumble", "Onyx", "Sable", "Fizz", "Juniper",
	"Cinder", "Wisp", "Pebble", "Halcyon", "Mango", "Drift",
	"Kestrel", "Umbra", "Saffron", "Nimbus", "Thistle", "Ember",
}

// NewWorld creates a populated arena with the player and MaxBots bots.
func NewWorld(seed int64) *World {
	w := &World{
		Rng:         rand.New(rand.NewSource(seed)),
		foodGrid:    NewGrid[int](),
		ejectaGrid:  NewGrid[int](),
		blobGrid:    NewGrid[*Blob](),
		FoodTarget:  MaxFood,
		BotTarget:   MaxBots,
		VirusTarget: MaxViruses,
	}

	w.Player = w.newOwner("You", false)
	w.Player.Color = Color{30, 144, 255}
	w.spawnBlobsFor(w.Player, PlayerStartMass)

	for i := 0; i < MaxBots; i++ {
		w.spawnBot(i)
	}

	for i := 0; i < MaxFood; i++ {
		w.Food = append(w.Food, w.newFood())
	}
	for i := 0; i < MaxViruses; i++ {
		if v := w.newVirus(); v != nil {
			w.Viruses = append(w.Viruses, v)
		}
	}

	return w
}

func (w *World) newOwner(name string, isBot bool) *Owner {
	w.nextOwnerID++
	o := &Owner{
		ID:    w.nextOwnerID,
		Name:  name,
		IsBot: isBot,
	}
	w.Owners = append(w.Owners, o)
	return o
}

func (w *World) spawnBot(i int) *Owner {
	name := botNames[i%len(botNames)]
	if i >= len(botNames) {
		name = fmt.Sprintf("%s %d", name, i/len(botNames)+1)
	}
	o := w.newOwner(name, true)
	o.Color = Color{
		uint8(60 + w.Rng.Intn(180)),
		uint8(60 + w.Rng.Intn(180)),
		uint8(60 + w.Rng.Intn(180)),
	}
	o.Brain = &Brain{NextThink: w.Rng.Float64() * BotThinkInterval}
	w.spawnBlobsFor(o, BotStartMass)
	return o
}

// spawnBlobsFor gives an owner a single fresh blob at a safe random location.
func (w *World) spawnBlobsFor(o *Owner, mass float64) {
	x, y := w.safeSpawnPoint(MassToRadius(mass))
	w.nextBlobID++
	o.Blobs = append(o.Blobs, &Blob{
		ID:      w.nextBlobID,
		OwnerID: o.ID,
		X:       x,
		Y:       y,
		Mass:    mass,
	})
	o.TargetX, o.TargetY = x, y
	o.Dead = false
}

// safeSpawnPoint is trySafeSpawnPoint with a random fallback, for entities that
// must be placed somewhere (a respawning player cannot simply not exist).
func (w *World) safeSpawnPoint(r float64) (float64, float64) {
	if x, y, ok := w.trySafeSpawnPoint(r); ok {
		return x, y
	}
	margin := r + 50
	return margin + w.Rng.Float64()*(WorldSize-2*margin),
		margin + w.Rng.Float64()*(WorldSize-2*margin)
}

// trySafeSpawnPoint looks for a spot with nothing large nearby, and reports
// whether it found one. Callers that can afford to wait (virus repopulation)
// should skip rather than force a placement: a virus dropped on top of a big
// cell pops it instantly, which reads as a random unprovoked explosion.
func (w *World) trySafeSpawnPoint(r float64) (float64, float64, bool) {
	margin := r + 50
	for try := 0; try < 30; try++ {
		x := margin + w.Rng.Float64()*(WorldSize-2*margin)
		y := margin + w.Rng.Float64()*(WorldSize-2*margin)
		ok := true
		for _, o := range w.Owners {
			for _, b := range o.Blobs {
				if dist(x, y, b.X, b.Y) < b.Radius()+r+150 {
					ok = false
					break
				}
			}
			if !ok {
				break
			}
		}
		if ok {
			for _, v := range w.Viruses {
				if dist(x, y, v.X, v.Y) < v.Radius()+r+50 {
					ok = false
					break
				}
			}
		}
		if ok {
			return x, y, true
		}
	}
	return 0, 0, false
}

func (w *World) newFood() *Food {
	return &Food{
		X:     w.Rng.Float64() * WorldSize,
		Y:     w.Rng.Float64() * WorldSize,
		Mass:  FoodMass,
		Color: foodColors[w.Rng.Intn(len(foodColors))],
	}
}

// newVirus returns a virus at a safe spot, or nil if the arena is too crowded
// to place one right now.
func (w *World) newVirus() *Virus {
	x, y, ok := w.trySafeSpawnPoint(VirusRadius)
	if !ok {
		return nil
	}
	return &Virus{
		X:     x,
		Y:     y,
		Mass:  VirusMass,
		Phase: w.Rng.Float64() * math.Pi * 2,
	}
}

// SetPlayerTarget points the player's blobs at a world coordinate. It is
// ignored under autopilot, where the brain sets the target instead.
func (w *World) SetPlayerTarget(x, y float64) {
	if w.Player.Brain != nil {
		return
	}
	w.Player.TargetX, w.Player.TargetY = x, y
}

// SetAutopilot hands the player over to the bot AI, or takes it back.
func (w *World) SetAutopilot(on bool) {
	if on && w.Player.Brain == nil {
		w.Player.Brain = &Brain{}
	} else if !on {
		w.Player.Brain = nil
	}
}

// Autopilot reports whether the AI is driving the player.
func (w *World) Autopilot() bool { return w.Player.Brain != nil }

// Leader returns the owner with the most mass, for the spectator camera.
func (w *World) Leader() *Owner {
	var best *Owner
	for _, o := range w.Owners {
		if o.Dead || len(o.Blobs) == 0 {
			continue
		}
		if best == nil || o.Mass() > best.Mass() {
			best = o
		}
	}
	return best
}

// Step advances the simulation by a fixed dt. Order matters: think, move,
// collide, decay, repopulate.
func (w *World) Step(dt float64) {
	w.Time += dt

	w.rebuildGrids()

	// Any owner with a Brain is AI-driven. That is normally every bot, but
	// attaching one to the player is what demo mode does.
	for _, o := range w.Owners {
		if o.Brain != nil && !o.Dead {
			w.thinkBot(o, dt)
		}
	}

	for _, o := range w.Owners {
		for _, b := range o.Blobs {
			stepBlob(b, o.TargetX, o.TargetY, dt)
			applyDecay(b, dt)
		}
	}
	for _, e := range w.Ejectas {
		stepEjecta(e, dt)
	}
	for _, v := range w.Viruses {
		stepVirus(v, dt)
	}

	// Same-owner interactions run before cross-owner ones so that a merge
	// completes before the merged blob is tested against enemies.
	for _, o := range w.Owners {
		w.resolveSiblings(o)
	}

	w.resolveFood()
	w.resolveEjecta()
	w.resolveViruses()
	w.resolveCells()

	w.maintain()

	if w.Player != nil && !w.Player.Dead {
		if m := w.Player.Mass(); m > w.PlayerBest {
			w.PlayerBest = m
		}
	}
}

// rebuildGrids repopulates the broad-phase structures for this tick.
func (w *World) rebuildGrids() {
	w.foodGrid.Clear()
	for i, f := range w.Food {
		w.foodGrid.Insert(f.X, f.Y, i)
	}
	w.ejectaGrid.Clear()
	for i, e := range w.Ejectas {
		w.ejectaGrid.Insert(e.X, e.Y, i)
	}
	w.blobGrid.Clear()
	for _, o := range w.Owners {
		for _, b := range o.Blobs {
			w.blobGrid.Insert(b.X, b.Y, b)
		}
	}
}

// maintain keeps populations topped up and clears out dead owners.
func (w *World) maintain() {
	for len(w.Food) < w.FoodTarget {
		w.Food = append(w.Food, w.newFood())
	}
	// Top up viruses one per tick at most, and only where there is room. A
	// failed placement simply waits for the next tick.
	if len(w.Viruses) < w.VirusTarget {
		if v := w.newVirus(); v != nil {
			w.Viruses = append(w.Viruses, v)
		}
	}

	botCount := 0
	for _, o := range w.Owners {
		if !o.IsBot {
			continue
		}
		if o.Dead {
			// Respawn in place rather than churning the owner list, so colors
			// and names stay stable across a match.
			w.spawnBlobsFor(o, BotStartMass)
			o.Brain.reset()
		}
		botCount++
	}
	for i := botCount; i < w.BotTarget; i++ {
		w.spawnBot(i)
	}
}

// RespawnPlayer restarts the player after death.
func (w *World) RespawnPlayer() {
	if !w.Player.Dead {
		return
	}
	w.PlayerDeaths++
	w.PlayerBest = 0
	w.spawnBlobsFor(w.Player, PlayerStartMass)
}

// LeaderEntry is one row of the leaderboard.
type LeaderEntry struct {
	Name     string
	Mass     float64
	IsPlayer bool
}

// Leaderboard returns the top n owners by total mass, largest first.
func (w *World) Leaderboard(n int) []LeaderEntry {
	entries := make([]LeaderEntry, 0, len(w.Owners))
	for _, o := range w.Owners {
		if o.Dead || len(o.Blobs) == 0 {
			continue
		}
		entries = append(entries, LeaderEntry{
			Name:     o.Name,
			Mass:     o.Mass(),
			IsPlayer: o == w.Player,
		})
	}
	// n is small (10) and len(entries) is ~16, so a selection sort is cheaper
	// than sort.Slice's interface overhead and allocates nothing.
	if n > len(entries) {
		n = len(entries)
	}
	for i := 0; i < n; i++ {
		best := i
		for j := i + 1; j < len(entries); j++ {
			if entries[j].Mass > entries[best].Mass {
				best = j
			}
		}
		entries[i], entries[best] = entries[best], entries[i]
	}
	return entries[:n]
}

// TotalBlobs counts every blob in the world, for diagnostics.
func (w *World) TotalBlobs() int {
	n := 0
	for _, o := range w.Owners {
		n += len(o.Blobs)
	}
	return n
}
