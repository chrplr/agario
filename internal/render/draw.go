package render

import (
	"fmt"
	"math"
	"sort"

	"agario/internal/game"
	"github.com/Zyko0/go-sdl3/sdl"
)

// Renderer owns the SDL resources and draws a World. All SDL calls in the
// program live in this package and main.go.
type Renderer struct {
	r    *sdl.Renderer
	disc *sdl.Texture
	cam  *Camera

	// blobOrder is reused each frame so sorting cells by mass allocates nothing.
	blobOrder []*game.Blob
	// virusVerts and virusIdx are scratch for RenderGeometry.
	virusVerts []sdl.Vertex
	virusIdx   []int32
}

var (
	colBackground = sdl.Color{R: 242, G: 243, B: 245, A: 255}
	colGrid       = sdl.Color{R: 223, G: 226, B: 230, A: 255}
	colBorder     = sdl.Color{R: 205, G: 72, B: 72, A: 255}
	colText       = sdl.Color{R: 40, G: 44, B: 52, A: 255}
	colTextLight  = sdl.Color{R: 255, G: 255, B: 255, A: 255}
	colVirus      = sdl.Color{R: 51, G: 201, B: 92, A: 255}
	colVirusEdge  = sdl.Color{R: 30, G: 150, B: 60, A: 255}
)

const gridSpacing = 50.0

func New(r *sdl.Renderer, cam *Camera) (*Renderer, error) {
	disc, err := MakeDisc(r)
	if err != nil {
		return nil, err
	}
	return &Renderer{r: r, disc: disc, cam: cam}, nil
}

func (rd *Renderer) Destroy() {
	if rd.disc != nil {
		rd.disc.Destroy()
		rd.disc = nil
	}
}

// disc draws the shared circle texture at a world position and radius.
func (rd *Renderer) drawDisc(wx, wy, wr float64, c game.Color, alpha uint8) {
	sx, sy := rd.cam.WorldToScreen(wx, wy)
	sr := float32(wr * rd.cam.Zoom)
	if sr < 0.5 {
		sr = 0.5
	}
	rd.disc.SetColorMod(c.R, c.G, c.B)
	rd.disc.SetAlphaMod(alpha)
	rd.r.RenderTexture(rd.disc, nil, &sdl.FRect{
		X: sx - sr, Y: sy - sr, W: sr * 2, H: sr * 2,
	})
}

func darken(c game.Color, amount float64) game.Color {
	f := 1 - amount
	return game.Color{
		R: uint8(float64(c.R) * f),
		G: uint8(float64(c.G) * f),
		B: uint8(float64(c.B) * f),
	}
}

// Draw renders one frame in the z-order given by the spec: grid, pellets,
// viruses, cells smallest-first, then labels. It does not present, so callers
// can read the framebuffer back (screenshot mode) before the swap makes its
// contents undefined.
func (rd *Renderer) Draw(w *game.World, fps float64, paused bool) {
	rd.r.SetDrawColor(colBackground.R, colBackground.G, colBackground.B, 255)
	rd.r.Clear()

	rd.drawGrid()
	rd.drawPellets(w)
	rd.drawViruses(w)
	rd.drawBlobs(w)
	rd.drawHUD(w, fps, paused)
}

// Present swaps the completed frame to the window.
func (rd *Renderer) Present() { rd.r.Present() }

// DrawWorld draws and presents a frame.
func (rd *Renderer) DrawWorld(w *game.World, fps float64, paused bool) {
	rd.Draw(w, fps, paused)
	rd.Present()
}

// drawGrid draws the background lattice and the arena boundary. Lines are
// stepped in world space and converted, so the grid slides and scales with the
// camera instead of being pinned to the window.
func (rd *Renderer) drawGrid() {
	cam := rd.cam
	rd.r.SetDrawColor(colGrid.R, colGrid.G, colGrid.B, 255)

	// World-space bounds of the current view.
	x0, y0 := cam.ScreenToWorld(0, 0)
	x1, y1 := cam.ScreenToWorld(cam.W, cam.H)

	startX := math.Floor(x0/gridSpacing) * gridSpacing
	for x := startX; x <= x1; x += gridSpacing {
		sx, _ := cam.WorldToScreen(x, 0)
		rd.r.RenderLine(sx, 0, sx, float32(cam.H))
	}
	startY := math.Floor(y0/gridSpacing) * gridSpacing
	for y := startY; y <= y1; y += gridSpacing {
		_, sy := cam.WorldToScreen(0, y)
		rd.r.RenderLine(0, sy, float32(cam.W), sy)
	}

	tlx, tly := cam.WorldToScreen(0, 0)
	brx, bry := cam.WorldToScreen(game.WorldSize, game.WorldSize)

	// Shade everything outside the arena. Without this the grid runs on past
	// the wall and it is not obvious where the playable area ends.
	rd.r.SetDrawBlendMode(sdl.BLENDMODE_BLEND)
	rd.r.SetDrawColor(120, 124, 132, 70)
	sw, sh := float32(cam.W), float32(cam.H)
	for _, r := range [4]sdl.FRect{
		{X: 0, Y: 0, W: sw, H: max32(0, tly)},                    // above
		{X: 0, Y: min32(sh, bry), W: sw, H: sh - min32(sh, bry)}, // below
		{X: 0, Y: 0, W: max32(0, tlx), H: sh},                    // left
		{X: min32(sw, brx), Y: 0, W: sw - min32(sw, brx), H: sh}, // right
	} {
		if r.W > 0 && r.H > 0 {
			rd.r.RenderFillRect(&r)
		}
	}

	// Arena border, drawn as a few nested rects to fake a thick line.
	rd.r.SetDrawColor(colBorder.R, colBorder.G, colBorder.B, 255)
	for i := float32(0); i < 4; i++ {
		rd.r.RenderRect(&sdl.FRect{X: tlx - i, Y: tly - i, W: brx - tlx + 2*i, H: bry - tly + 2*i})
	}
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func (rd *Renderer) drawPellets(w *game.World) {
	for _, f := range w.Food {
		if !rd.cam.Visible(f.X, f.Y, game.FoodRadius) {
			continue
		}
		rd.drawDisc(f.X, f.Y, game.FoodRadius, f.Color, 255)
	}
	for _, e := range w.Ejectas {
		if !rd.cam.Visible(e.X, e.Y, game.EjectaRadius) {
			continue
		}
		rd.drawDisc(e.X, e.Y, game.EjectaRadius, darken(e.Color, 0.15), 255)
	}
}

// drawViruses draws the spiked green hazards. The disc texture cannot express
// spikes, so these use RenderGeometry with an alternating inner/outer rim.
func (rd *Renderer) drawViruses(w *game.World) {
	const spikes = 22

	for _, v := range w.Viruses {
		r := v.Radius()
		if !rd.cam.Visible(v.X, v.Y, r*1.15) {
			continue
		}
		cx, cy := rd.cam.WorldToScreen(v.X, v.Y)
		outer := float32(r * 1.12 * rd.cam.Zoom)
		inner := float32(r * 0.94 * rd.cam.Zoom)

		rd.buildStar(cx, cy, inner, outer, spikes, v.Phase, colVirusEdge)
		rd.r.RenderGeometry(nil, rd.virusVerts, rd.virusIdx)

		rd.buildStar(cx, cy, inner*0.86, outer*0.86, spikes, v.Phase, colVirus)
		rd.r.RenderGeometry(nil, rd.virusVerts, rd.virusIdx)
	}
}

// buildStar fills the scratch buffers with a triangle list forming a spiked
// disc. RenderGeometry takes a triangle list, not a fan, so every triangle
// names all three of its vertices.
func (rd *Renderer) buildStar(cx, cy, inner, outer float32, spikes int, phase float64, c sdl.Color) {
	n := spikes * 2
	col := sdl.FColor{
		R: float32(c.R) / 255,
		G: float32(c.G) / 255,
		B: float32(c.B) / 255,
		A: 1,
	}

	rd.virusVerts = rd.virusVerts[:0]
	rd.virusIdx = rd.virusIdx[:0]

	// Vertex 0 is the center; 1..n are the rim, alternating outer and inner.
	rd.virusVerts = append(rd.virusVerts, sdl.Vertex{
		Position: sdl.FPoint{X: cx, Y: cy}, Color: col,
	})
	for i := 0; i < n; i++ {
		a := phase + 2*math.Pi*float64(i)/float64(n)
		rr := outer
		if i%2 == 1 {
			rr = inner
		}
		rd.virusVerts = append(rd.virusVerts, sdl.Vertex{
			Position: sdl.FPoint{
				X: cx + rr*float32(math.Cos(a)),
				Y: cy + rr*float32(math.Sin(a)),
			},
			Color: col,
		})
	}
	for i := 0; i < n; i++ {
		next := i + 2
		if i == n-1 {
			next = 1
		}
		rd.virusIdx = append(rd.virusIdx, 0, int32(i+1), int32(next))
	}
}

// drawBlobs draws every cell smallest-first so big cells overlap small ones,
// then labels them.
func (rd *Renderer) drawBlobs(w *game.World) {
	rd.blobOrder = rd.blobOrder[:0]
	for _, o := range w.Owners {
		for _, b := range o.Blobs {
			if rd.cam.Visible(b.X, b.Y, b.Radius()) {
				rd.blobOrder = append(rd.blobOrder, b)
			}
		}
	}
	sort.Slice(rd.blobOrder, func(i, j int) bool {
		return rd.blobOrder[i].Mass < rd.blobOrder[j].Mass
	})

	owners := make(map[uint32]*game.Owner, len(w.Owners))
	for _, o := range w.Owners {
		owners[o.ID] = o
	}

	for _, b := range rd.blobOrder {
		o := owners[b.OwnerID]
		if o == nil {
			continue
		}
		r := b.Radius()
		// Two passes give the rimmed agar.io look from one texture: a darker
		// disc at full radius, then the fill slightly inside it.
		rd.drawDisc(b.X, b.Y, r, darken(o.Color, 0.28), 255)
		rd.drawDisc(b.X, b.Y, r*0.9, o.Color, 255)
	}

	// Labels last, so they are never covered by a cell drawn afterwards.
	for _, b := range rd.blobOrder {
		o := owners[b.OwnerID]
		if o == nil {
			continue
		}
		sr := b.Radius() * rd.cam.Zoom
		if sr < 22 {
			continue
		}
		sx, sy := rd.cam.WorldToScreen(b.X, b.Y)

		nameScale := float32(math.Max(1, math.Min(4, sr/26)))
		massScale := nameScale * 0.7
		TextCentered(rd.r, sx, sy-CharW*nameScale*0.7, nameScale, colTextLight, o.Name)
		TextCentered(rd.r, sx, sy+CharW*massScale*0.9, massScale, colTextLight,
			fmt.Sprintf("%d", int(b.Mass)))
	}
}

func (rd *Renderer) drawHUD(w *game.World, fps float64, paused bool) {
	const pad = 10

	Text(rd.r, pad, pad, 2, colText, fmt.Sprintf("MASS %d", int(w.Player.Mass())))
	Text(rd.r, pad, pad+22, 1.5, colText, fmt.Sprintf("BEST %d", int(w.PlayerBest)))
	Text(rd.r, pad, float32(rd.cam.H)-40, 1.5, colText, fmt.Sprintf("FPS %d", int(fps+0.5)))
	Text(rd.r, pad, float32(rd.cam.H)-22, 1.5, colText,
		"MOUSE steer  SPACE split  W eject  P pause  T autopilot  ESC quit")

	if w.Autopilot() {
		TextCentered(rd.r, float32(rd.cam.W/2), 18, 2, colBorder, "AUTOPILOT")
	}

	rd.drawLeaderboard(w)
	rd.drawMinimap(w)

	if w.Player.Dead {
		cx, cy := float32(rd.cam.W/2), float32(rd.cam.H/2)
		TextCentered(rd.r, cx, cy-20, 4, colBorder, "EATEN")
		TextCentered(rd.r, cx, cy+20, 2, colText, "press R to respawn")
	} else if paused {
		TextCentered(rd.r, float32(rd.cam.W/2), float32(rd.cam.H/2), 4, colText, "PAUSED")
	}
}

func (rd *Renderer) drawLeaderboard(w *game.World) {
	entries := w.Leaderboard(10)
	const scale = 1.5
	// Wide enough for the longest row the format below can produce:
	// "10 Marmalade   123456" is 21 glyphs.
	width := TextWidth("00 12345678901 1234567", scale)
	x := float32(rd.cam.W) - width - 10
	y := float32(10)

	rd.r.SetDrawColor(252, 252, 253, 236)
	rd.r.SetDrawBlendMode(sdl.BLENDMODE_BLEND)
	rd.r.RenderFillRect(&sdl.FRect{X: x - 6, Y: y - 6, W: width + 12, H: float32(len(entries))*18 + 34})

	Text(rd.r, x, y, scale, colText, "LEADERBOARD")
	y += 22
	for i, e := range entries {
		c := colText
		if e.IsPlayer {
			c = sdl.Color{R: 30, G: 110, B: 200, A: 255}
		}
		name := e.Name
		if len(name) > 11 {
			name = name[:11]
		}
		Text(rd.r, x, y, scale, c, fmt.Sprintf("%2d %-11s %d", i+1, name, int(e.Mass)))
		y += 18
	}
}

// drawMinimap shows the whole arena in a corner box, with one dot per owner.
func (rd *Renderer) drawMinimap(w *game.World) {
	const size = 150
	x := float32(rd.cam.W) - size - 10
	y := float32(rd.cam.H) - size - 10
	scale := float32(size) / float32(game.WorldSize)

	rd.r.SetDrawBlendMode(sdl.BLENDMODE_BLEND)
	rd.r.SetDrawColor(252, 252, 253, 232)
	rd.r.RenderFillRect(&sdl.FRect{X: x, Y: y, W: size, H: size})
	rd.r.SetDrawColor(colBorder.R, colBorder.G, colBorder.B, 200)
	rd.r.RenderRect(&sdl.FRect{X: x, Y: y, W: size, H: size})

	for _, v := range w.Viruses {
		rd.r.SetDrawColor(colVirus.R, colVirus.G, colVirus.B, 200)
		rd.r.RenderFillRect(&sdl.FRect{
			X: x + float32(v.X)*scale - 1, Y: y + float32(v.Y)*scale - 1, W: 3, H: 3,
		})
	}
	for _, o := range w.Owners {
		if len(o.Blobs) == 0 {
			continue
		}
		ox, oy := o.Center()
		s := float32(2)
		if o == w.Player {
			s = 4
		}
		rd.r.SetDrawColor(o.Color.R, o.Color.G, o.Color.B, 255)
		rd.r.RenderFillRect(&sdl.FRect{
			X: x + float32(ox)*scale - s/2, Y: y + float32(oy)*scale - s/2, W: s, H: s,
		})
	}

	// Viewport rectangle, so the minimap shows where you are looking. It is
	// clipped to the arena: when zoomed out the view is wider than the world
	// and the raw rect would spill outside the minimap box.
	vx0, vy0 := rd.cam.ScreenToWorld(0, 0)
	vx1, vy1 := rd.cam.ScreenToWorld(rd.cam.W, rd.cam.H)
	vx0 = math.Max(0, vx0)
	vy0 = math.Max(0, vy0)
	vx1 = math.Min(game.WorldSize, vx1)
	vy1 = math.Min(game.WorldSize, vy1)
	rd.r.SetDrawColor(60, 60, 60, 120)
	rd.r.RenderRect(&sdl.FRect{
		X: x + float32(vx0)*scale, Y: y + float32(vy0)*scale,
		W: float32(vx1-vx0) * scale, H: float32(vy1-vy0) * scale,
	})
}
