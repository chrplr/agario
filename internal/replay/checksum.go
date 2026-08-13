// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package replay

import (
	"math"

	"agario/internal/game"
)

// FNV-1a 64-bit, mixing whole uint64 words rather than bytes: eight times fewer
// multiplies for the same avalanche on values that are all already 64 bits.
const (
	fnvOffset uint64 = 14695981039346656037
	fnvPrime  uint64 = 1099511628211
)

type hasher uint64

func newHasher() hasher { return hasher(fnvOffset) }

func (h *hasher) u64(v uint64) {
	*h ^= hasher(v)
	*h *= hasher(fnvPrime)
}

// f64 hashes the raw IEEE-754 bits. This is a bit-exactness check, not a
// tolerance check, because bit-exactness is what replay actually needs: a
// divergence of one ulp compounds into a different world within seconds.
func (h *hasher) f64(v float64) { h.u64(math.Float64bits(v)) }

func (h *hasher) int(v int) { h.u64(uint64(v)) }

func (h *hasher) bool(v bool) {
	if v {
		h.u64(1)
	} else {
		h.u64(0)
	}
}

// Checksum hashes every exported field of the world that Step reads or writes,
// in slice order — slice order is itself simulation state, so it is hashed
// rather than sorted around.
//
// Three pieces of state are unexported and so not covered: World.nextOwnerID,
// World.nextBlobID and Brain.splitCooldown. Widening internal/game's API for
// them is not worth it, because each surfaces in covered state within a tick or
// two of any divergence — a wrong nextBlobID appears as a wrong Blob.ID on the
// next split, a wrong splitCooldown as a different blob count.
//
// The RNG cursor is not covered either; math/rand exposes no way to read it.
// It does not need to be: maintain() draws from it on almost every tick to top
// up food, so a cursor slip moves a pellet immediately.
func Checksum(w *game.World) uint64 {
	h := newHasher()

	h.f64(w.Time)
	h.int(w.PlayerDeaths)
	h.f64(w.PlayerBest)
	h.int(w.FoodTarget)
	h.int(w.BotTarget)
	h.int(w.VirusTarget)

	h.int(len(w.Owners))
	for _, o := range w.Owners {
		h.u64(uint64(o.ID))
		h.bool(o.IsBot)
		h.bool(o.Dead)
		h.f64(o.TargetX)
		h.f64(o.TargetY)
		// A brain appearing or disappearing is exactly what an autopilot
		// desync looks like, so its presence is part of the hash.
		h.bool(o.Brain != nil)
		if o.Brain != nil {
			h.f64(o.Brain.NextThink)
			h.f64(o.Brain.GoalX)
			h.f64(o.Brain.GoalY)
		}
		h.int(len(o.Blobs))
		for _, b := range o.Blobs {
			h.u64(uint64(b.ID))
			h.u64(uint64(b.OwnerID))
			h.f64(b.X)
			h.f64(b.Y)
			h.f64(b.VX)
			h.f64(b.VY)
			h.f64(b.Mass)
			h.f64(b.MergeAt)
		}
	}

	h.int(len(w.Food))
	for _, f := range w.Food {
		h.f64(f.X)
		h.f64(f.Y)
		h.f64(f.Mass)
	}

	h.int(len(w.Ejectas))
	for _, e := range w.Ejectas {
		h.f64(e.X)
		h.f64(e.Y)
		h.f64(e.VX)
		h.f64(e.VY)
		h.f64(e.Mass)
		h.u64(uint64(e.OwnerID))
		h.f64(e.GraceEnd)
	}

	h.int(len(w.Viruses))
	for _, v := range w.Viruses {
		h.f64(v.X)
		h.f64(v.Y)
		h.f64(v.VX)
		h.f64(v.VY)
		h.f64(v.Mass)
		h.int(v.Fed)
		h.f64(v.Phase)
	}

	return uint64(h)
}

// Witnesses summarises the world in the few scalars that make a divergence
// legible. They are written next to the hash in every checkpoint.
func Witnesses(w *game.World) Witness {
	var mass float64
	for _, o := range w.Owners {
		mass += o.Mass()
	}
	return Witness{
		Time:   w.Time,
		Mass:   mass,
		Blobs:  w.TotalBlobs(),
		Food:   len(w.Food),
		Ejecta: len(w.Ejectas),
		Virus:  len(w.Viruses),
		Deaths: w.PlayerDeaths,
	}
}
