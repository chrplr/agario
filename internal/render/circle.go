// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"github.com/Zyko0/go-sdl3/sdl"
)

// discSize is the resolution of the antialiased disc texture. Every circle in
// the game is this one texture, color-modulated and scaled, so it wants to be
// comfortably larger than a typical on-screen cell.
const discSize = 256

// MakeDisc builds a white antialiased circle texture. Coverage is computed by
// 4x4 supersampling per pixel, which gives a clean edge that survives being
// scaled up to a large cell.
func MakeDisc(renderer *sdl.Renderer) (*sdl.Texture, error) {
	const sub = 4
	pixels := make([]byte, discSize*discSize*4)

	center := float64(discSize) / 2
	radius := center - 1 // leave a pixel of margin so the edge never clips

	for y := 0; y < discSize; y++ {
		for x := 0; x < discSize; x++ {
			hits := 0
			for sy := 0; sy < sub; sy++ {
				for sx := 0; sx < sub; sx++ {
					px := float64(x) + (float64(sx)+0.5)/sub
					py := float64(y) + (float64(sy)+0.5)/sub
					dx, dy := px-center, py-center
					if dx*dx+dy*dy <= radius*radius {
						hits++
					}
				}
			}
			i := (y*discSize + x) * 4
			// White RGB with coverage in alpha: SetColorMod then tints it.
			pixels[i+0] = 255
			pixels[i+1] = 255
			pixels[i+2] = 255
			pixels[i+3] = byte(hits * 255 / (sub * sub))
		}
	}

	tex, err := renderer.CreateTexture(sdl.PIXELFORMAT_RGBA32, sdl.TEXTUREACCESS_STATIC, discSize, discSize)
	if err != nil {
		return nil, err
	}
	if err := tex.Update(nil, pixels, discSize*4); err != nil {
		tex.Destroy()
		return nil, err
	}
	if err := tex.SetBlendMode(sdl.BLENDMODE_BLEND); err != nil {
		tex.Destroy()
		return nil, err
	}
	if err := tex.SetScaleMode(sdl.SCALEMODE_LINEAR); err != nil {
		tex.Destroy()
		return nil, err
	}
	return tex, nil
}
