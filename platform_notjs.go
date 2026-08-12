// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

//go:build !js

package main

import "github.com/Zyko0/go-sdl3/sdl"

// This file and platform_js.go carry every difference between the native and
// browser builds. The call sites in main.go stay identical on both targets;
// only these helpers change. That is the same shape go-sdl3 itself uses for
// binsdl.Load(), which is a no-op struct on js.

// setVSync asks the renderer to sync to the display refresh.
func setVSync(r *sdl.Renderer) { r.SetVSync(1) }

// initialSize is the window size to create. Natively that is whatever -w/-h say.
func initialSize() (int, int) { return *flagWidth, *flagHeight }

// canSaveFiles reports whether the process can write to a filesystem, which
// gates the -shot screenshot mode.
func canSaveFiles() bool { return true }
