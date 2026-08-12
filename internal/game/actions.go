package game

import "math"

// Split halves every eligible blob, largest first, until the blob budget runs
// out. Iterating a snapshot matters: without it a blob split this tick would be
// re-split immediately, cascading to MaxBlobs from a single keypress.
func (w *World) Split(o *Owner) {
	if o.Dead || len(o.Blobs) >= MaxBlobs {
		return
	}

	snapshot := make([]*Blob, len(o.Blobs))
	copy(snapshot, o.Blobs)
	// Largest first, so a split spends the budget where it does the most good.
	for i := 0; i < len(snapshot); i++ {
		best := i
		for j := i + 1; j < len(snapshot); j++ {
			if snapshot[j].Mass > snapshot[best].Mass {
				best = j
			}
		}
		snapshot[i], snapshot[best] = snapshot[best], snapshot[i]
	}

	dirX, dirY := o.TargetX, o.TargetY
	for _, b := range snapshot {
		if len(o.Blobs) >= MaxBlobs {
			break
		}
		if b.Mass < SplitMinMass {
			continue
		}
		half := b.Mass / 2
		b.Mass = half

		ux, uy := unitToward(b.X, b.Y, dirX, dirY)
		if ux == 0 && uy == 0 {
			ux = 1
		}

		w.nextBlobID++
		child := &Blob{
			ID:      w.nextBlobID,
			OwnerID: o.ID,
			X:       b.X + ux*b.Radius()*0.5,
			Y:       b.Y + uy*b.Radius()*0.5,
			VX:      ux * SplitImpulse,
			VY:      uy * SplitImpulse,
			Mass:    half,
		}
		clampToArena(&child.X, &child.Y, child.Radius())

		// Both halves get the cooldown; only stamping the child would let the
		// parent re-absorb it the moment they touched.
		cd := w.Time + MergeDelay(half)
		b.MergeAt = cd
		child.MergeAt = cd

		o.Blobs = append(o.Blobs, child)
	}
}

// Eject spits a pellet from each eligible blob toward the owner's target.
func (w *World) Eject(o *Owner) {
	if o.Dead {
		return
	}
	for _, b := range o.Blobs {
		if b.Mass < EjectMinMass {
			continue
		}
		ux, uy := unitToward(b.X, b.Y, o.TargetX, o.TargetY)
		if ux == 0 && uy == 0 {
			ux = 1
		}
		b.Mass -= EjectCost

		r := b.Radius()
		e := &Ejecta{
			X:        b.X + ux*(r+EjectaRadius+2),
			Y:        b.Y + uy*(r+EjectaRadius+2),
			VX:       ux * EjectImpulse,
			VY:       uy * EjectImpulse,
			Mass:     EjectaMass,
			Color:    o.Color,
			OwnerID:  o.ID,
			GraceEnd: w.Time + 0.4,
		}
		clampToArena(&e.X, &e.Y, EjectaRadius)
		w.Ejectas = append(w.Ejectas, e)
	}
}

// popOnVirus shatters a blob that ran into a virus, fanning the pieces outward.
// This is the spec's "split into maximum possible sub-cells" (section 4, row 3).
func (w *World) popOnVirus(o *Owner, b *Blob, v *Virus) {
	// The virus's own mass is absorbed, which is why popping is survivable.
	b.Mass += v.Mass

	budget := MaxBlobs - len(o.Blobs)
	if budget <= 0 {
		return
	}
	// Never shed pieces below the minimum viable mass.
	pieces := budget
	if maxByMass := int(b.Mass/MinBlobMass) - 1; pieces > maxByMass {
		pieces = maxByMass
	}
	if pieces <= 0 {
		return
	}

	total := b.Mass
	share := total / float64(pieces+1)
	b.Mass = share

	cd := w.Time + MergeDelay(share)
	b.MergeAt = cd

	start := w.Rng.Float64() * 2 * math.Pi
	for i := 0; i < pieces; i++ {
		a := start + 2*math.Pi*float64(i)/float64(pieces)
		ux, uy := math.Cos(a), math.Sin(a)
		w.nextBlobID++
		child := &Blob{
			ID:      w.nextBlobID,
			OwnerID: o.ID,
			X:       b.X + ux*b.Radius()*0.4,
			Y:       b.Y + uy*b.Radius()*0.4,
			VX:      ux * SplitImpulse * 0.8,
			VY:      uy * SplitImpulse * 0.8,
			Mass:    share,
			MergeAt: cd,
		}
		clampToArena(&child.X, &child.Y, child.Radius())
		o.Blobs = append(o.Blobs, child)
	}
}

// unitToward returns the unit vector from (x, y) to (tx, ty), or (0, 0) if they
// coincide.
func unitToward(x, y, tx, ty float64) (float64, float64) {
	dx, dy := tx-x, ty-y
	d := math.Hypot(dx, dy)
	if d < 1e-6 {
		return 0, 0
	}
	return dx / d, dy / d
}
