// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package game

import "math"

// resolveFood consumes pellets whose center falls inside a blob (spec section 4,
// row 1). Eaten pellets are swapped out and replaced by maintain().
func (w *World) resolveFood() {
	w.eatenFood = w.eatenFood[:0]
	eaten := make(map[int]bool)

	for _, o := range w.Owners {
		for _, b := range o.Blobs {
			r := b.Radius()
			w.foodGrid.ForEachNear(b.X, b.Y, r, func(i int) {
				if eaten[i] {
					return
				}
				f := w.Food[i]
				if dist2(b.X, b.Y, f.X, f.Y) <= r*r {
					b.Mass = math.Min(b.Mass+f.Mass, MaxBlobMassCap)
					eaten[i] = true
					w.eatenFood = append(w.eatenFood, i)
				}
			})
		}
	}
	removeIndices(&w.Food, w.eatenFood)
}

// resolveEjecta lets cells eat loose pellets, respecting the short grace period
// that keeps a cell from re-swallowing its own ejection instantly.
func (w *World) resolveEjecta() {
	w.eatenEjecta = w.eatenEjecta[:0]
	eaten := make(map[int]bool)

	for _, o := range w.Owners {
		for _, b := range o.Blobs {
			r := b.Radius()
			w.ejectaGrid.ForEachNear(b.X, b.Y, r, func(i int) {
				if eaten[i] {
					return
				}
				e := w.Ejectas[i]
				if e.OwnerID == o.ID && w.Time < e.GraceEnd {
					return
				}
				if dist2(b.X, b.Y, e.X, e.Y) <= r*r {
					b.Mass = math.Min(b.Mass+e.Mass, MaxBlobMassCap)
					eaten[i] = true
					w.eatenEjecta = append(w.eatenEjecta, i)
				}
			})
		}
	}
	removeIndices(&w.Ejectas, w.eatenEjecta)
}

// resolveCells handles cross-owner consumption (spec section 4, row 2).
func (w *World) resolveCells() {
	type kill struct {
		eater  *Blob
		victim *Blob
		owner  *Owner
	}
	var kills []kill
	dead := make(map[*Blob]bool)

	for _, o := range w.Owners {
		for _, b := range o.Blobs {
			if dead[b] {
				continue
			}
			r := b.Radius()
			w.blobGrid.ForEachNear(b.X, b.Y, r, func(other *Blob) {
				if other.OwnerID == b.OwnerID || dead[other] || dead[b] {
					return
				}
				d := dist(b.X, b.Y, other.X, other.Y)
				if CanEat(b.Mass, other.Mass, d) {
					dead[other] = true
					kills = append(kills, kill{eater: b, victim: other, owner: o})
				}
			})
		}
	}

	for _, k := range kills {
		k.eater.Mass = math.Min(k.eater.Mass+k.victim.Mass, MaxBlobMassCap)
		w.removeBlobByID(k.victim)
	}
}

// resolveViruses pops oversized cells and feeds viruses ejected pellets
// (spec section 4, rows 3 and 4).
func (w *World) resolveViruses() {
	// Cell vs virus. Collect first: popping mutates the owner's blob slice.
	type hit struct {
		owner *Owner
		blob  *Blob
		virus int
	}
	var hits []hit
	usedVirus := make(map[int]bool)
	poppedBlob := make(map[*Blob]bool)

	for _, o := range w.Owners {
		for _, b := range o.Blobs {
			if poppedBlob[b] {
				continue
			}
			for vi, v := range w.Viruses {
				if usedVirus[vi] {
					continue
				}
				// Small cells slide over viruses untouched, which is what makes
				// a virus useful cover when you are the prey.
				if b.Mass <= VirusPopMinMass {
					continue
				}
				d := dist(b.X, b.Y, v.X, v.Y)
				if d <= b.Radius()-EatOverlapCoef*v.Radius() {
					usedVirus[vi] = true
					poppedBlob[b] = true
					hits = append(hits, hit{owner: o, blob: b, virus: vi})
					break
				}
			}
		}
	}

	var consumedViruses []int
	for _, h := range hits {
		w.popOnVirus(h.owner, h.blob, w.Viruses[h.virus])
		consumedViruses = append(consumedViruses, h.virus)
	}

	// Ejecta vs virus: absorption and reproduction.
	var absorbed []int
	for ei, e := range w.Ejectas {
		for vi, v := range w.Viruses {
			if usedVirus[vi] {
				continue
			}
			if dist(e.X, e.Y, v.X, v.Y) <= v.Radius()+EjectaRadius {
				absorbed = append(absorbed, ei)
				v.Fed++
				if v.Fed >= VirusFeedCount {
					v.Fed = 0
					w.reproduceVirus(v, e.VX, e.VY)
				}
				break
			}
		}
	}

	removeIndices(&w.Ejectas, absorbed)
	removeIndices(&w.Viruses, consumedViruses)
}

// reproduceVirus shoots a new virus along the direction of the pellet that fed
// it. A fed pellet whose velocity has already decayed picks a random heading
// rather than stacking a virus on top of its parent.
func (w *World) reproduceVirus(parent *Virus, vx, vy float64) {
	d := math.Hypot(vx, vy)
	var ux, uy float64
	if d < 1e-6 {
		a := w.Rng.Float64() * 2 * math.Pi
		ux, uy = math.Cos(a), math.Sin(a)
	} else {
		ux, uy = vx/d, vy/d
	}
	child := &Virus{
		X:     parent.X + ux*parent.Radius(),
		Y:     parent.Y + uy*parent.Radius(),
		VX:    ux * VirusShotSpeed,
		VY:    uy * VirusShotSpeed,
		Mass:  VirusMass,
		Phase: w.Rng.Float64() * math.Pi * 2,
	}
	clampToArena(&child.X, &child.Y, child.Radius())
	w.Viruses = append(w.Viruses, child)
}

// resolveSiblings merges or separates blobs belonging to the same owner
// (spec section 4, rows 5 and 6).
func (w *World) resolveSiblings(o *Owner) {
	if len(o.Blobs) < 2 {
		return
	}

	merged := make(map[*Blob]bool)
	for i := 0; i < len(o.Blobs); i++ {
		a := o.Blobs[i]
		if merged[a] {
			continue
		}
		for j := i + 1; j < len(o.Blobs); j++ {
			b := o.Blobs[j]
			if merged[b] {
				continue
			}
			d := dist(a.X, a.Y, b.X, b.Y)
			ra, rb := a.Radius(), b.Radius()

			ready := w.Time >= a.MergeAt && w.Time >= b.MergeAt
			if ready && d <= math.Max(ra, rb) {
				// Absorb the smaller into the larger, conserving mass.
				big, small := a, b
				if b.Mass > a.Mass {
					big, small = b, a
				}
				big.Mass += small.Mass
				merged[small] = true
				continue
			}

			// Soft separation, but only while the cooldown is active (spec
			// section 4, last row). Pushing merge-ready blobs apart as well
			// would hold them at d ~= ra+rb, which is outside the merge
			// threshold, and they would never recombine at all.
			if ready {
				continue
			}
			if overlap := ra + rb - d; overlap > 0 && d > 1e-6 {
				push := overlap * 0.5 * 0.35
				nx, ny := (a.X-b.X)/d, (a.Y-b.Y)/d
				a.X += nx * push
				a.Y += ny * push
				b.X -= nx * push
				b.Y -= ny * push
				clampToArena(&a.X, &a.Y, ra)
				clampToArena(&b.X, &b.Y, rb)
			}
		}
	}

	if len(merged) > 0 {
		kept := o.Blobs[:0]
		for _, b := range o.Blobs {
			if !merged[b] {
				kept = append(kept, b)
			}
		}
		o.Blobs = kept
	}
}

// removeBlobByID drops a blob from whichever owner holds it and marks that
// owner dead if it was their last.
func (w *World) removeBlobByID(target *Blob) {
	for _, o := range w.Owners {
		for i, b := range o.Blobs {
			if b == target {
				o.Blobs = append(o.Blobs[:i], o.Blobs[i+1:]...)
				if len(o.Blobs) == 0 {
					o.Dead = true
				}
				return
			}
		}
	}
}

// removeIndices deletes the given (unsorted, possibly duplicated) indices from a
// slice in place, preserving nothing about order.
func removeIndices[T any](slice *[]T, idx []int) {
	if len(idx) == 0 {
		return
	}
	drop := make(map[int]bool, len(idx))
	for _, i := range idx {
		drop[i] = true
	}
	s := *slice
	kept := s[:0]
	for i, v := range s {
		if !drop[i] {
			kept = append(kept, v)
		}
	}
	*slice = kept
}
