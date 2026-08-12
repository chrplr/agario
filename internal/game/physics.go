package game

import "math"

// stepBlob advances one blob by dt: steering toward its owner's target, plus a
// separately decaying impulse velocity from splits and virus pops.
func stepBlob(b *Blob, targetX, targetY, dt float64) {
	r := b.Radius()

	dx, dy := targetX-b.X, targetY-b.Y
	d := math.Hypot(dx, dy)
	if d > 1e-6 {
		speed := Speed(b.Mass)
		// Ease off as the target gets inside the blob, so a cell parked under
		// the cursor sits still instead of vibrating across it. A ramp does
		// this more smoothly than a hard deadzone, which visibly snaps.
		if d < r {
			speed *= d / r
		}
		b.X += dx / d * speed * dt
		b.Y += dy / d * speed * dt
	}

	// Impulse velocity: exponential decay, framerate-independent.
	if b.VX != 0 || b.VY != 0 {
		b.X += b.VX * dt
		b.Y += b.VY * dt
		damp := math.Exp(-ImpulseDecay * dt)
		b.VX *= damp
		b.VY *= damp
		if math.Abs(b.VX) < 1 && math.Abs(b.VY) < 1 {
			b.VX, b.VY = 0, 0
		}
	}

	clampToArena(&b.X, &b.Y, r)
}

// stepEjecta advances a free pellet, which has no steering, only decay.
func stepEjecta(e *Ejecta, dt float64) {
	if e.VX == 0 && e.VY == 0 {
		return
	}
	e.X += e.VX * dt
	e.Y += e.VY * dt
	damp := math.Exp(-ImpulseDecay * dt)
	e.VX *= damp
	e.VY *= damp
	if math.Abs(e.VX) < 1 && math.Abs(e.VY) < 1 {
		e.VX, e.VY = 0, 0
	}
	clampToArena(&e.X, &e.Y, EjectaRadius)
}

// stepVirus advances a virus launched by reproduction.
func stepVirus(v *Virus, dt float64) {
	if v.VX == 0 && v.VY == 0 {
		return
	}
	v.X += v.VX * dt
	v.Y += v.VY * dt
	damp := math.Exp(-ImpulseDecay * 0.5 * dt)
	v.VX *= damp
	v.VY *= damp
	if math.Abs(v.VX) < 1 && math.Abs(v.VY) < 1 {
		v.VX, v.VY = 0, 0
	}
	clampToArena(&v.X, &v.Y, v.Radius())
}

// clampToArena keeps an entity of the given radius inside the world bounds.
func clampToArena(x, y *float64, r float64) {
	// A blob wider than the arena would invert the clamp range; cap the margin.
	m := math.Min(r, WorldSize/2)
	*x = clamp(*x, m, WorldSize-m)
	*y = clamp(*y, m, WorldSize-m)
}

// applyDecay shrinks a blob above the decay threshold. The rate is a fraction
// of mass lost per second, applied continuously so it does not depend on dt.
func applyDecay(b *Blob, dt float64) {
	if b.Mass <= DecayMinMass {
		return
	}
	b.Mass *= math.Pow(1-DecayRate, dt)
	if b.Mass < DecayMinMass {
		b.Mass = DecayMinMass
	}
}
