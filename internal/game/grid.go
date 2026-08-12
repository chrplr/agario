// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

package game

// Grid is a uniform spatial hash over the arena, rebuilt each tick. It exists
// so that scanning 800 food pellets against every blob is proportional to the
// queried radius instead of O(cells * food).
//
// The generic parameter keeps one implementation for food, ejecta and blobs.
type Grid[T any] struct {
	cols, rows int
	buckets    [][]T
}

// NewGrid builds an empty grid covering [0, WorldSize] in both axes.
func NewGrid[T any]() *Grid[T] {
	cols := int(WorldSize/GridCell) + 1
	rows := cols
	return &Grid[T]{
		cols:    cols,
		rows:    rows,
		buckets: make([][]T, cols*rows),
	}
}

// Clear empties every bucket but keeps the backing arrays, so a rebuild each
// tick allocates nothing after the first few frames.
func (g *Grid[T]) Clear() {
	for i := range g.buckets {
		g.buckets[i] = g.buckets[i][:0]
	}
}

func (g *Grid[T]) index(x, y float64) int {
	cx := int(clamp(x/GridCell, 0, float64(g.cols-1)))
	cy := int(clamp(y/GridCell, 0, float64(g.rows-1)))
	return cy*g.cols + cx
}

// Insert places an item at a world position.
func (g *Grid[T]) Insert(x, y float64, item T) {
	i := g.index(x, y)
	g.buckets[i] = append(g.buckets[i], item)
}

// ForEachNear calls fn for every item in any bucket overlapping the square of
// half-width r around (x, y). Callers must still do an exact distance test:
// the grid is a broad phase, not a precise query.
func (g *Grid[T]) ForEachNear(x, y, r float64, fn func(T)) {
	minX := int(clamp((x-r)/GridCell, 0, float64(g.cols-1)))
	maxX := int(clamp((x+r)/GridCell, 0, float64(g.cols-1)))
	minY := int(clamp((y-r)/GridCell, 0, float64(g.rows-1)))
	maxY := int(clamp((y+r)/GridCell, 0, float64(g.rows-1)))
	for cy := minY; cy <= maxY; cy++ {
		row := cy * g.cols
		for cx := minX; cx <= maxX; cx++ {
			for _, item := range g.buckets[row+cx] {
				fn(item)
			}
		}
	}
}

// CountAt returns how many items share the bucket containing (x, y). Bots use
// it as a cheap local-density estimate when choosing which pellet to chase.
func (g *Grid[T]) CountAt(x, y float64) int {
	return len(g.buckets[g.index(x, y)])
}
