package game

import (
	"math"
	"testing"
)

const eps = 1e-9

// newBareWorld returns a world with no bots, food or viruses, so a test can set
// up an exact scenario without background activity perturbing it.
func newBareWorld(t *testing.T) *World {
	t.Helper()
	w := NewWorld(1)
	w.Owners = w.Owners[:0]
	w.Food = nil
	w.Viruses = nil
	w.Ejectas = nil
	// Freeze every population so background respawns cannot perturb a scenario
	// that is being measured exactly.
	w.FoodTarget, w.BotTarget, w.VirusTarget = 0, 0, 0
	w.Player = w.newOwner("You", false)
	return w
}

func (w *World) addBlob(o *Owner, x, y, mass float64) *Blob {
	w.nextBlobID++
	b := &Blob{ID: w.nextBlobID, OwnerID: o.ID, X: x, Y: y, Mass: mass}
	o.Blobs = append(o.Blobs, b)
	return b
}

func TestMassToRadiusMonotonic(t *testing.T) {
	prev := MassToRadius(1)
	for m := 2.0; m < 5000; m *= 1.3 {
		r := MassToRadius(m)
		if r <= prev {
			t.Fatalf("radius not increasing at mass %v: %v <= %v", m, r, prev)
		}
		prev = r
	}
	if got := MassToRadius(25); math.Abs(got-50) > eps {
		t.Errorf("MassToRadius(25) = %v, want 50", got)
	}
}

func TestSpeedDecreasesWithMassAndHitsFloor(t *testing.T) {
	if Speed(25) <= Speed(2500) {
		t.Errorf("heavier cell is not slower: %v vs %v", Speed(25), Speed(2500))
	}
	// The floor must only bite at genuinely huge masses, otherwise the whole
	// speed curve collapses into a constant.
	if Speed(PlayerStartMass) <= MinSpeed {
		t.Errorf("starting mass already at speed floor: %v", Speed(PlayerStartMass))
	}
	if got := Speed(1e12); got != MinSpeed {
		t.Errorf("Speed(1e12) = %v, want floor %v", got, MinSpeed)
	}
}

func TestCanEatBoundaries(t *testing.T) {
	// Mass ratio boundary: exactly 1.25x is edible, a hair under is not.
	mb := 100.0
	ma := mb * EatMassRatio
	if !CanEat(ma, mb, 0) {
		t.Error("exactly 1.25x mass at zero distance should be edible")
	}
	if CanEat(ma-eps*ma, mb, 0) {
		t.Error("just under 1.25x should not be edible")
	}

	// Distance boundary: dist <= Ra - 0.33*Rb.
	ma = 400
	ra, rb := MassToRadius(ma), MassToRadius(mb)
	limit := ra - EatOverlapCoef*rb
	if !CanEat(ma, mb, limit) {
		t.Errorf("distance exactly at the limit %v should be edible", limit)
	}
	if CanEat(ma, mb, limit+1) {
		t.Error("beyond the overlap limit should not be edible")
	}
}

func TestSplitConservesMassAndRespectsCap(t *testing.T) {
	w := newBareWorld(t)
	o := w.Player
	w.addBlob(o, 1000, 1000, 200)
	o.TargetX, o.TargetY = 2000, 1000

	before := o.Mass()
	w.Split(o)

	if len(o.Blobs) != 2 {
		t.Fatalf("expected 2 blobs after split, got %d", len(o.Blobs))
	}
	if math.Abs(o.Mass()-before) > eps {
		t.Errorf("split changed total mass: %v -> %v", before, o.Mass())
	}
	for _, b := range o.Blobs {
		if math.Abs(b.Mass-100) > eps {
			t.Errorf("uneven split: blob mass %v, want 100", b.Mass)
		}
		// Both halves must carry the cooldown, or the parent re-absorbs the
		// child the instant they overlap.
		if b.MergeAt <= w.Time {
			t.Errorf("blob has no merge cooldown: MergeAt=%v Time=%v", b.MergeAt, w.Time)
		}
	}

	// Repeated splits must never exceed the cap.
	for i := 0; i < 10; i++ {
		w.Split(o)
	}
	if len(o.Blobs) > MaxBlobs {
		t.Errorf("blob count %d exceeds cap %d", len(o.Blobs), MaxBlobs)
	}
	if math.Abs(o.Mass()-before) > 1e-6 {
		t.Errorf("repeated splits changed mass: %v -> %v", before, o.Mass())
	}
}

func TestSplitDoesNotCascade(t *testing.T) {
	// A single Split on one big blob must produce exactly two blobs, not a
	// chain reaction of the freshly created halves splitting again.
	w := newBareWorld(t)
	o := w.Player
	w.addBlob(o, 1000, 1000, 10000)
	o.TargetX, o.TargetY = 2000, 1000

	w.Split(o)
	if len(o.Blobs) != 2 {
		t.Fatalf("one Split produced %d blobs, want 2", len(o.Blobs))
	}
}

func TestSplitBelowMinimumDoesNothing(t *testing.T) {
	w := newBareWorld(t)
	o := w.Player
	w.addBlob(o, 1000, 1000, SplitMinMass-1)
	w.Split(o)
	if len(o.Blobs) != 1 {
		t.Errorf("blob below SplitMinMass split anyway: %d blobs", len(o.Blobs))
	}
}

func TestMergeRespectsCooldownAndConservesMass(t *testing.T) {
	w := newBareWorld(t)
	o := w.Player
	a := w.addBlob(o, 1000, 1000, 100)
	b := w.addBlob(o, 1005, 1000, 100) // heavily overlapping
	a.MergeAt = 10
	b.MergeAt = 10

	w.Time = 5 // cooldown still active
	w.resolveSiblings(o)
	if len(o.Blobs) != 2 {
		t.Fatalf("blobs merged before cooldown expired")
	}
	// They must have been pushed apart instead of stacking exactly.
	if dist(a.X, a.Y, b.X, b.Y) <= 5 {
		t.Errorf("overlapping blobs were not pushed apart: d=%v", dist(a.X, a.Y, b.X, b.Y))
	}

	w.Time = 11 // cooldown expired
	a.X, a.Y, b.X, b.Y = 1000, 1000, 1005, 1000
	before := o.Mass()
	w.resolveSiblings(o)
	if len(o.Blobs) != 1 {
		t.Fatalf("blobs did not merge after cooldown: %d blobs", len(o.Blobs))
	}
	if math.Abs(o.Mass()-before) > eps {
		t.Errorf("merge changed mass: %v -> %v", before, o.Mass())
	}
}

func TestEjectCostsMassAndSpawnsPellet(t *testing.T) {
	w := newBareWorld(t)
	o := w.Player
	b := w.addBlob(o, 1000, 1000, 100)
	o.TargetX, o.TargetY = 2000, 1000

	w.Eject(o)
	if len(w.Ejectas) != 1 {
		t.Fatalf("expected 1 ejecta, got %d", len(w.Ejectas))
	}
	if math.Abs(b.Mass-(100-EjectCost)) > eps {
		t.Errorf("blob mass %v, want %v", b.Mass, 100-EjectCost)
	}
	e := w.Ejectas[0]
	if e.X <= b.X {
		t.Errorf("ejecta spawned behind the blob: e.X=%v b.X=%v", e.X, b.X)
	}
	if e.VX <= 0 {
		t.Errorf("ejecta not moving toward the target: VX=%v", e.VX)
	}
	// The gap between cost and pellet mass is the game's mass sink.
	if EjectaMass >= EjectCost {
		t.Errorf("ejecting is not a mass sink: pellet %v >= cost %v", EjectaMass, EjectCost)
	}
}

func TestEjectBelowMinimumDoesNothing(t *testing.T) {
	w := newBareWorld(t)
	o := w.Player
	w.addBlob(o, 1000, 1000, EjectMinMass-1)
	w.Eject(o)
	if len(w.Ejectas) != 0 {
		t.Errorf("blob below EjectMinMass ejected anyway")
	}
}

func TestVirusPopShattersAndConservesMass(t *testing.T) {
	w := newBareWorld(t)
	o := w.Player
	b := w.addBlob(o, 1000, 1000, 2000)
	v := &Virus{X: 1000, Y: 1000, Mass: VirusMass}

	before := o.Mass() + v.Mass
	w.popOnVirus(o, b, v)

	if len(o.Blobs) < 2 {
		t.Fatalf("virus pop produced only %d blobs", len(o.Blobs))
	}
	if len(o.Blobs) > MaxBlobs {
		t.Fatalf("virus pop produced %d blobs, over cap %d", len(o.Blobs), MaxBlobs)
	}
	if math.Abs(o.Mass()-before) > 1e-6 {
		t.Errorf("virus pop changed mass: %v -> %v", before, o.Mass())
	}
	for _, blob := range o.Blobs {
		if blob.MergeAt <= w.Time {
			t.Errorf("popped piece has no merge cooldown")
		}
	}
}

func TestSmallCellIgnoresVirus(t *testing.T) {
	// A cell at or below the pop threshold must pass over a virus untouched:
	// this is what makes viruses usable as cover when you are small.
	w := newBareWorld(t)
	o := w.Player
	w.addBlob(o, 1000, 1000, VirusPopMinMass)
	w.Viruses = []*Virus{{X: 1000, Y: 1000, Mass: VirusMass}}

	w.resolveViruses()

	if len(o.Blobs) != 1 {
		t.Errorf("small cell was popped by a virus: %d blobs", len(o.Blobs))
	}
	if len(w.Viruses) != 1 {
		t.Errorf("virus was consumed by a cell too small to pop it")
	}
}

func TestVirusReproducesAfterFeeding(t *testing.T) {
	w := newBareWorld(t)
	v := &Virus{X: 2000, Y: 2000, Mass: VirusMass}
	w.Viruses = []*Virus{v}

	for i := 0; i < VirusFeedCount; i++ {
		w.Ejectas = append(w.Ejectas, &Ejecta{
			X: v.X, Y: v.Y, VX: 100, VY: 0, Mass: EjectaMass,
		})
		w.rebuildGrids()
		w.resolveViruses()
	}

	if len(w.Viruses) != 2 {
		t.Fatalf("virus did not reproduce after %d feedings: %d viruses",
			VirusFeedCount, len(w.Viruses))
	}
	if len(w.Ejectas) != 0 {
		t.Errorf("fed ejecta were not absorbed: %d left", len(w.Ejectas))
	}
	if v.Fed != 0 {
		t.Errorf("feed counter not reset after reproducing: %d", v.Fed)
	}
}

func TestCellConsumptionTransfersMassAndKillsOwner(t *testing.T) {
	w := newBareWorld(t)
	big := w.newOwner("Big", false)
	small := w.newOwner("Small", false)
	w.addBlob(big, 1000, 1000, 500)
	w.addBlob(small, 1000, 1000, 50)

	w.rebuildGrids()
	w.resolveCells()

	if len(small.Blobs) != 0 {
		t.Fatalf("small cell survived: %d blobs", len(small.Blobs))
	}
	if !small.Dead {
		t.Error("owner with no blobs left was not marked dead")
	}
	if math.Abs(big.Mass()-550) > eps {
		t.Errorf("eater mass %v, want 550", big.Mass())
	}
}

func TestFoodIsEatenOnceOnly(t *testing.T) {
	// Two overlapping cells must not both collect the same pellet.
	w := newBareWorld(t)
	a := w.newOwner("A", false)
	b := w.newOwner("B", false)
	w.addBlob(a, 1000, 1000, 100)
	w.addBlob(b, 1000, 1000, 100)
	w.Food = []*Food{{X: 1000, Y: 1000, Mass: FoodMass}}

	w.rebuildGrids()
	w.resolveFood()

	total := a.Mass() + b.Mass()
	if math.Abs(total-(200+FoodMass)) > eps {
		t.Errorf("pellet was double-counted: total mass %v, want %v", total, 200+FoodMass)
	}
	if len(w.Food) != 0 {
		t.Errorf("eaten pellet was not removed")
	}
}

func TestGridMatchesBruteForce(t *testing.T) {
	w := NewWorld(99)
	w.rebuildGrids()

	queries := []struct{ x, y, r float64 }{
		{0, 0, 100}, {2000, 2000, 500}, {3999, 3999, 300},
		{1234, 4321 - 400, 1000}, {2000, 2000, 4000},
	}
	for _, q := range queries {
		got := map[int]bool{}
		w.foodGrid.ForEachNear(q.x, q.y, q.r, func(i int) {
			f := w.Food[i]
			if dist(q.x, q.y, f.X, f.Y) <= q.r {
				got[i] = true
			}
		})
		want := map[int]bool{}
		for i, f := range w.Food {
			if dist(q.x, q.y, f.X, f.Y) <= q.r {
				want[i] = true
			}
		}
		if len(got) != len(want) {
			t.Errorf("query (%v,%v,r=%v): grid found %d, brute force %d",
				q.x, q.y, q.r, len(got), len(want))
			continue
		}
		for i := range want {
			if !got[i] {
				t.Errorf("query (%v,%v,r=%v): grid missed pellet %d", q.x, q.y, q.r, i)
			}
		}
	}
}

func TestBlobsStayInsideArena(t *testing.T) {
	w := NewWorld(5)
	for i := 0; i < 5000; i++ {
		// Steer hard into a corner.
		w.SetPlayerTarget(-10000, -10000)
		w.Step(TickDT)
	}
	for _, o := range w.Owners {
		for _, b := range o.Blobs {
			if b.X < 0 || b.X > WorldSize || b.Y < 0 || b.Y > WorldSize {
				t.Fatalf("blob escaped the arena at (%v, %v)", b.X, b.Y)
			}
		}
	}
}

func TestDecayOnlyAboveThreshold(t *testing.T) {
	small := &Blob{Mass: DecayMinMass}
	applyDecay(small, 1)
	if small.Mass != DecayMinMass {
		t.Errorf("blob at the threshold decayed: %v", small.Mass)
	}

	big := &Blob{Mass: 5000}
	applyDecay(big, 1)
	if big.Mass >= 5000 {
		t.Errorf("large blob did not decay: %v", big.Mass)
	}
	// Decay must be framerate-independent: one 1s step equals 120 ticks.
	a := &Blob{Mass: 5000}
	applyDecay(a, 1)
	b := &Blob{Mass: 5000}
	for i := 0; i < 120; i++ {
		applyDecay(b, TickDT)
	}
	if math.Abs(a.Mass-b.Mass) > 1e-6 {
		t.Errorf("decay depends on step size: %v vs %v", a.Mass, b.Mass)
	}
}

func TestDeterministicGivenSameSeed(t *testing.T) {
	run := func() (float64, int, float64) {
		w := NewWorld(1234)
		for i := 0; i < 10000; i++ {
			w.SetPlayerTarget(float64(i%4000), float64((i*3)%4000))
			if i%1000 == 500 {
				w.Split(w.Player)
			}
			w.Step(TickDT)
		}
		var sum float64
		for _, o := range w.Owners {
			sum += o.Mass()
		}
		return sum, w.TotalBlobs(), w.Time
	}
	m1, b1, t1 := run()
	m2, b2, t2 := run()
	if m1 != m2 || b1 != b2 || t1 != t2 {
		t.Errorf("same seed diverged: (%v,%d,%v) vs (%v,%d,%v)", m1, b1, t1, m2, b2, t2)
	}
}

func TestLeaderboardSortedDescending(t *testing.T) {
	w := NewWorld(8)
	for i := 0; i < 3000; i++ {
		w.Step(TickDT)
	}
	entries := w.Leaderboard(10)
	if len(entries) == 0 {
		t.Fatal("empty leaderboard")
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].Mass > entries[i-1].Mass {
			t.Errorf("leaderboard not descending at %d: %v > %v",
				i, entries[i].Mass, entries[i-1].Mass)
		}
	}
}

// warmWorld returns a world advanced to a representative mid-game state: bots
// grown, split and scattered, rather than 16 identical starting cells.
func warmWorld(seed int64) *World {
	w := NewWorld(seed)
	for i := 0; i < 12000; i++ { // 100 simulated seconds
		w.Step(TickDT)
	}
	return w
}

// BenchmarkStep measures a bounded-age world. Letting a single world run for
// the whole benchmark makes the result depend on b.N: cells keep growing and
// splitting, per-tick cost climbs, and two runs with different iteration counts
// are not comparable. Recycling every 3000 ticks keeps the measured state
// inside a fixed window.
func BenchmarkStep(b *testing.B) {
	const window = 3000

	w := warmWorld(17)
	ticks := 0

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Step(TickDT)
		if ticks++; ticks == window {
			b.StopTimer()
			w = warmWorld(17)
			ticks = 0
			b.StartTimer()
		}
	}
}
