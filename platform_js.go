// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

//go:build js

package main

import (
	"syscall/js"

	"github.com/Zyko0/go-sdl3/sdl"
)

// Browser variants of the platform helpers. See platform_notjs.go.

// setVSync does nothing in the browser. SDL_SetRenderVSync has no js binding,
// and requestAnimationFrame already paces the frame loop to the display.
func setVSync(r *sdl.Renderer) {}

// initialSize returns the browser viewport size, so the game fills the page
// rather than sitting in a fixed box.
//
// SDL's web backend derives the window size from the canvas CSS box, which
// go-sdl3 sets from these dimensions at window creation. There is no resize
// path afterwards, so resizing the browser window takes effect on reload.
func initialSize() (int, int) {
	w := js.Global().Get("innerWidth").Int()
	h := js.Global().Get("innerHeight").Int()
	if w <= 0 || h <= 0 {
		return *flagWidth, *flagHeight
	}
	return w, h
}

// canSaveFiles is false in the browser: there is no filesystem to write a
// screenshot to.
func canSaveFiles() bool { return false }
