package game

import "math"

// Brain holds a bot's persistent decision state. Re-deciding every tick makes
// bots twitch; re-deciding only once a second makes them oblivious to anything
// that happens in between. A short interval plus a goal that persists between
// evaluations gives movement that reads as deliberate.
type Brain struct {
	NextThink float64
	// GoalX, GoalY is the committed steering target between evaluations.
	GoalX, GoalY float64
	// splitCooldown stops a bot from chain-splitting itself into confetti.
	splitCooldown float64
}

func (b *Brain) reset() {
	b.NextThink = 0
	b.splitCooldown = 0
}

// bestFoodTarget picks the pellet within fov that maximizes local density over
// distance, so a bot prefers a nearby cluster to a lone distant pellet but
// always aims at something it can actually eat.
func (w *World) bestFoodTarget(cx, cy, fov float64) (float64, float64, bool) {
	best := -1.0
	var bx, by float64
	found := false
	w.foodGrid.ForEachNear(cx, cy, fov, func(i int) {
		f := w.Food[i]
		d := dist(cx, cy, f.X, f.Y)
		if d > fov {
			return
		}
		score := (1 + 0.2*float64(w.foodGrid.CountAt(f.X, f.Y))) / (d + 50)
		if score > best {
			best, bx, by, found = score, f.X, f.Y, true
		}
	})
	return bx, by, found
}

// thinkBot updates one bot's target, and may trigger a split.
func (w *World) thinkBot(o *Owner, dt float64) {
	br := o.Brain
	if br.splitCooldown > 0 {
		br.splitCooldown -= dt
	}

	br.NextThink -= dt
	if br.NextThink > 0 {
		o.TargetX, o.TargetY = br.GoalX, br.GoalY
		return
	}
	br.NextThink = BotThinkInterval

	cx, cy := o.Center()
	mass := o.Mass()
	big := o.LargestBlob()
	if big == nil {
		return
	}
	fov := clamp(big.Radius()*BotFOVCoef, BotFOVMin, BotFOVMax)

	// Accumulate a steering vector. Threats dominate, but everything is summed
	// so a bot fleeing danger still drifts toward food rather than into a wall.
	var sx, sy float64
	var threatWeight float64
	var bestPrey *Blob
	var bestPreyDist = math.Inf(1)

	w.blobGrid.ForEachNear(cx, cy, fov, func(other *Blob) {
		if other.OwnerID == o.ID {
			return
		}
		d := dist(cx, cy, other.X, other.Y)
		if d > fov || d < 1e-6 {
			return
		}
		ux, uy := (cx-other.X)/d, (cy-other.Y)/d

		switch {
		case other.Mass > big.Mass*BotThreatRatio:
			// Flee. Weight rises sharply as the threat closes.
			wgt := 900 * (1 - d/fov) * (1 - d/fov)
			sx += ux * wgt
			sy += uy * wgt
			threatWeight += wgt
		case other.Mass < big.Mass*BotPreyRatio:
			// Chase. Prefer the closest edible target.
			if d < bestPreyDist {
				bestPreyDist = d
				bestPrey = other
			}
		}
	})

	// Viruses are obstacles only once the bot is big enough to be popped.
	if big.Mass > VirusPopMinMass {
		for _, v := range w.Viruses {
			d := dist(cx, cy, v.X, v.Y)
			if d > fov || d < 1e-6 {
				continue
			}
			wgt := 700 * (1 - d/fov) * (1 - d/fov)
			sx += (cx - v.X) / d * wgt
			sy += (cy - v.Y) / d * wgt
		}
	}

	// Walls: without this bots pin themselves into corners while fleeing.
	if cx < BotWallMargin {
		sx += (BotWallMargin - cx) * 2
	}
	if cx > WorldSize-BotWallMargin {
		sx -= (cx - (WorldSize - BotWallMargin)) * 2
	}
	if cy < BotWallMargin {
		sy += (BotWallMargin - cy) * 2
	}
	if cy > WorldSize-BotWallMargin {
		sy -= (cy - (WorldSize - BotWallMargin)) * 2
	}

	switch {
	case threatWeight > 0:
		// Under threat, run: normalize and project well past the arena so the
		// blobs commit to the escape heading.
		d := math.Hypot(sx, sy)
		if d < 1e-6 {
			sx, sy = 1, 0
			d = 1
		}
		br.GoalX = cx + sx/d*fov
		br.GoalY = cy + sy/d*fov

	case bestPrey != nil:
		br.GoalX, br.GoalY = bestPrey.X, bestPrey.Y
		// Aggressive split: only when clearly dominant, in range, and with
		// enough mass left over that each half still outclasses the prey.
		if br.splitCooldown <= 0 &&
			len(o.Blobs) < MaxBlobs/2 &&
			big.Mass >= SplitMinMass &&
			big.Mass/2 > bestPrey.Mass*BotSplitRatio &&
			bestPreyDist < (big.Radius()+bestPrey.Radius())*1.6 {
			w.Split(o)
			br.splitCooldown = 4
		}

	default:
		// Graze. Target an actual pellet, not a bucket center: a bot sent to
		// the middle of a bucket parks on empty ground, eats nothing, and
		// re-picks the same bucket forever. The density term still biases it
		// toward clusters.
		if fx, fy, ok := w.bestFoodTarget(cx, cy, math.Min(fov, BotFoodRange)); ok {
			br.GoalX, br.GoalY = fx, fy
		} else if sx != 0 || sy != 0 {
			br.GoalX, br.GoalY = cx+sx, cy+sy
		} else {
			// Nothing in sight: wander to a random point and stick with it.
			br.GoalX = w.Rng.Float64() * WorldSize
			br.GoalY = w.Rng.Float64() * WorldSize
		}
	}

	// Nudge scattered sub-cells back together once they can merge.
	if len(o.Blobs) > 1 && mass > 0 {
		allReady := true
		for _, b := range o.Blobs {
			if w.Time < b.MergeAt {
				allReady = false
				break
			}
		}
		if allReady && threatWeight == 0 && bestPrey == nil {
			br.GoalX, br.GoalY = cx, cy
		}
	}

	br.GoalX = clamp(br.GoalX, 0, WorldSize)
	br.GoalY = clamp(br.GoalY, 0, WorldSize)
	o.TargetX, o.TargetY = br.GoalX, br.GoalY
}
