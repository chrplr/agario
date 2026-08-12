package game

import "math"

// Color is an RGB triple. The game package does no rendering, but entities own
// their colors so the renderer stays a pure function of world state.
type Color struct{ R, G, B uint8 }

// Blob is one physical circle. An owner may control up to MaxBlobs of them.
type Blob struct {
	ID      uint32
	OwnerID uint32
	X, Y    float64
	// VX, VY is impulse velocity from splitting or being popped by a virus.
	// It decays to zero and is separate from steering movement.
	VX, VY float64
	Mass   float64
	// MergeAt is the world clock time at which this blob may recombine with a
	// sibling. Zero means "always mergeable" (a blob that never split).
	MergeAt float64
}

func (b *Blob) Radius() float64 { return MassToRadius(b.Mass) }

// Owner is a player or a bot: everything one participant controls.
type Owner struct {
	ID    uint32
	Name  string
	Color Color
	IsBot bool
	Blobs []*Blob

	// TargetX, TargetY is the world-space point every blob steers toward.
	TargetX, TargetY float64

	Brain *Brain // nil for the human player

	// Dead is set when the last blob is consumed.
	Dead bool
}

// Mass is the owner's total mass across all blobs.
func (o *Owner) Mass() float64 {
	var m float64
	for _, b := range o.Blobs {
		m += b.Mass
	}
	return m
}

// Center returns the mass-weighted centroid of the owner's blobs, which is what
// the camera follows and what the AI reasons about.
func (o *Owner) Center() (float64, float64) {
	var mx, my, total float64
	for _, b := range o.Blobs {
		mx += b.X * b.Mass
		my += b.Y * b.Mass
		total += b.Mass
	}
	if total == 0 {
		return WorldSize / 2, WorldSize / 2
	}
	return mx / total, my / total
}

// LargestBlob returns the owner's biggest blob, or nil if it has none.
func (o *Owner) LargestBlob() *Blob {
	var best *Blob
	for _, b := range o.Blobs {
		if best == nil || b.Mass > best.Mass {
			best = b
		}
	}
	return best
}

// removeBlob drops a blob from the owner by identity.
func (o *Owner) removeBlob(target *Blob) {
	for i, b := range o.Blobs {
		if b == target {
			o.Blobs = append(o.Blobs[:i], o.Blobs[i+1:]...)
			return
		}
	}
}

// Food is a static pellet.
type Food struct {
	X, Y  float64
	Mass  float64
	Color Color
}

// Ejecta is a pellet shot out by a cell. It is edible like food, and viruses
// absorb it to reproduce.
type Ejecta struct {
	X, Y   float64
	VX, VY float64
	Mass   float64
	Color  Color
	// OwnerID plus a short grace period stops a cell from instantly re-eating
	// what it just spat out.
	OwnerID  uint32
	GraceEnd float64
}

// Virus is a static spiked hazard that shatters large cells.
type Virus struct {
	X, Y   float64
	VX, VY float64
	Mass   float64
	// Fed counts absorbed ejecta; at VirusFeedCount the virus reproduces.
	Fed int
	// Phase offsets the spike pattern so viruses don't all look identical.
	Phase float64
}

func (v *Virus) Radius() float64 { return MassToRadius(v.Mass) }

// MassToRadius converts mass to a visual and collision radius.
func MassToRadius(m float64) float64 {
	if m <= 0 {
		return 0
	}
	return math.Sqrt(m * MassRadiusScale)
}

// Speed returns steering speed in world units per second. Heavier is slower.
func Speed(m float64) float64 {
	if m <= 0 {
		return MinSpeed
	}
	return math.Max(MinSpeed, SpeedBase/math.Pow(m, SpeedExp))
}

// MergeDelay is how long after splitting a blob of this mass may recombine.
func MergeDelay(m float64) float64 {
	return MergeBase + m*MergeMassCoef
}

// CanEat reports whether a blob of mass ma at distance d can consume a target of
// mass mb (spec section 4): enough mass advantage, and enough overlap.
func CanEat(ma, mb, d float64) bool {
	if ma < mb*EatMassRatio {
		return false
	}
	return d <= MassToRadius(ma)-EatOverlapCoef*MassToRadius(mb)
}

func dist(x1, y1, x2, y2 float64) float64 {
	return math.Hypot(x1-x2, y1-y2)
}

func dist2(x1, y1, x2, y2 float64) float64 {
	dx, dy := x1-x2, y1-y2
	return dx*dx + dy*dy
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
