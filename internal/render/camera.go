package render

import (
	"math"

	"agario/internal/game"
)

// Camera maps world coordinates to screen pixels. Both the center and the zoom
// are smoothed toward their targets: without smoothing the view snaps hard
// every time the player splits or merges, which is disorienting.
type Camera struct {
	W, H             float64
	CenterX, CenterY float64
	Zoom             float64

	targetX, targetY float64
	targetZoom       float64
}

// Zoom is chosen so a starting cell sees roughly 1400 world units across the
// window, and clamped so a huge cell does not zoom out to a featureless dot.
const (
	zoomCoef = 4.6
	zoomMin  = 0.22
	zoomMax  = 1.0

	// Exponential smoothing rate, per second.
	followRate = 8.0
	zoomRate   = 3.0
)

func NewCamera(w, h int32) *Camera {
	c := &Camera{
		W:       float64(w),
		H:       float64(h),
		CenterX: game.WorldSize / 2,
		CenterY: game.WorldSize / 2,
		Zoom:    1,
	}
	c.targetX, c.targetY, c.targetZoom = c.CenterX, c.CenterY, 1
	return c
}

func (c *Camera) Resize(w, h int32) {
	c.W, c.H = float64(w), float64(h)
}

// Follow aims the camera at an owner's center of mass, at a zoom set by its
// total mass.
func (c *Camera) Follow(o *game.Owner, dt float64) {
	if len(o.Blobs) > 0 {
		c.targetX, c.targetY = o.Center()
		mass := o.Mass()
		if mass < 1 {
			mass = 1
		}
		c.targetZoom = math.Max(zoomMin, math.Min(zoomMax, zoomCoef/math.Sqrt(mass)))
	}

	// Frame-rate independent exponential approach.
	fp := 1 - math.Exp(-followRate*dt)
	c.CenterX += (c.targetX - c.CenterX) * fp
	c.CenterY += (c.targetY - c.CenterY) * fp

	zp := 1 - math.Exp(-zoomRate*dt)
	c.Zoom += (c.targetZoom - c.Zoom) * zp

	c.clampToWorld()
}

// clampToWorld keeps the viewport inside the arena so a cell in a corner does
// not fill half the screen with empty out-of-bounds space. When the view is
// wider than the arena there is nothing to clamp, so it just centers.
func (c *Camera) clampToWorld() {
	halfW := c.W / 2 / c.Zoom
	halfH := c.H / 2 / c.Zoom

	if halfW*2 >= game.WorldSize {
		c.CenterX = game.WorldSize / 2
	} else {
		c.CenterX = math.Max(halfW, math.Min(game.WorldSize-halfW, c.CenterX))
	}
	if halfH*2 >= game.WorldSize {
		c.CenterY = game.WorldSize / 2
	} else {
		c.CenterY = math.Max(halfH, math.Min(game.WorldSize-halfH, c.CenterY))
	}
}

// Snap jumps straight to the target, used on respawn where smoothing would
// drag the view across the whole arena.
func (c *Camera) Snap(o *game.Owner) {
	if len(o.Blobs) == 0 {
		return
	}
	c.CenterX, c.CenterY = o.Center()
	mass := math.Max(1, o.Mass())
	c.Zoom = math.Max(zoomMin, math.Min(zoomMax, zoomCoef/math.Sqrt(mass)))
	c.targetX, c.targetY, c.targetZoom = c.CenterX, c.CenterY, c.Zoom
	c.clampToWorld()
}

func (c *Camera) WorldToScreen(wx, wy float64) (float32, float32) {
	return float32((wx-c.CenterX)*c.Zoom + c.W/2),
		float32((wy-c.CenterY)*c.Zoom + c.H/2)
}

func (c *Camera) ScreenToWorld(sx, sy float64) (float64, float64) {
	return (sx-c.W/2)/c.Zoom + c.CenterX,
		(sy-c.H/2)/c.Zoom + c.CenterY
}

// Visible reports whether a world-space circle intersects the viewport, so the
// draw loop can skip the majority of 800 food pellets each frame.
func (c *Camera) Visible(wx, wy, r float64) bool {
	sx, sy := c.WorldToScreen(wx, wy)
	sr := float32(r*c.Zoom) + 2
	return sx+sr >= 0 && sx-sr <= float32(c.W) && sy+sr >= 0 && sy-sr <= float32(c.H)
}
