package render

import "github.com/Zyko0/go-sdl3/sdl"

// CharW is the width and height of one SDL debug-font glyph cell.
const CharW = float32(sdl.DEBUG_TEXT_FONT_CHARACTER_SIZE)

// Text draws a string at a scale. SetScale multiplies every coordinate the
// renderer sees, so the position has to be divided back out and the scale reset
// afterwards, or everything drawn later inherits it.
func Text(r *sdl.Renderer, x, y, scale float32, c sdl.Color, s string) {
	if scale <= 0 || s == "" {
		return
	}
	r.SetDrawColor(c.R, c.G, c.B, c.A)
	r.SetScale(scale, scale)
	r.DebugText(x/scale, y/scale, s)
	r.SetScale(1, 1)
}

// TextCentered draws a string centered on (x, y).
func TextCentered(r *sdl.Renderer, x, y, scale float32, c sdl.Color, s string) {
	w := float32(len(s)) * CharW * scale
	Text(r, x-w/2, y-CharW*scale/2, scale, c, s)
}

// TextWidth is the rendered width of a string at a scale.
func TextWidth(s string, scale float32) float32 {
	return float32(len(s)) * CharW * scale
}
