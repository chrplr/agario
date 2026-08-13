// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

// Package replay records a play session and plays it back.
//
// # The determinism contract
//
// A recording stores no world state. It stores the seed and the player's
// inputs, and replay reproduces everything else — bots included — by
// re-simulating. That works because *game.World is mutated by exactly two
// things: Step(TickDT), and the five action methods (SetPlayerTarget, Split,
// Eject, RespawnPlayer, SetAutopilot). Nothing else reaches the simulation:
// not the camera, not the frame rate, not the window size, not the clock.
//
// Three consequences shape this package:
//
//   - Record the world target handed to SetPlayerTarget, never mouse pixels.
//     The camera smooths its position using real frame time, so the same pixel
//     maps to a different world point on a different machine or window size.
//     Recording the derived target makes a replay independent of all three.
//
//   - Record actions at the site that mutates the world, not at the keystroke.
//     The recorder then sees only what actually reached the simulation, and the
//     guards around each key (paused, dead) stop mattering. No-ops are recorded
//     too: a Split that early-returns at MaxBlobs is data about intent.
//
//   - Order and multiplicity are the whole game. Every action perturbs the
//     shared RNG cursor that food respawn, virus spawning and bot wander all
//     draw from, so split,split is not split, and split,eject is not
//     eject,split. One extra or missing draw desynchronises the world
//     permanently.
//
// # What is not promised
//
// Bit-exact replay across machines. math.Exp and math.Pow run every tick and
// take CPU-feature-gated assembly paths on amd64, and differ across GOARCH and
// Go version. The header records goos/goarch/go/game_version so a mismatch can
// be reported as what it is rather than as a bug in this package. Checkpoints
// (see Checksum) detect divergence wherever it comes from.
package replay

// Version is the replay-file format version. Open refuses a file it does not
// understand. Bump on any incompatible change, as agarienv.Protocol does.
const Version = 1

// Magic identifies a replay file. Gzip's own 1f 8b is the outer magic for a
// compressed one.
const Magic = "agario-replay"

// DefaultChecksumEvery is one checkpoint per simulated second at 120 Hz: about
// 0.2% of a tick amortised, cheap enough to leave on always, tight enough to
// name the second a divergence started in. Use 1 once something has already
// diverged and the exact tick matters.
const DefaultChecksumEvery = 120

// Record kinds, the "k" field every line carries.
const (
	KindHeader     = "hdr"
	KindFrame      = "t"
	KindCheckpoint = "ck"
	KindEnd        = "end"
)

// Action is one discrete thing the player did. Autopilot is recorded as an
// absolute state rather than a toggle so a reader can never be out of phase
// with the recording.
type Action string

const (
	ActSplit        Action = "split"
	ActEject        Action = "eject"
	ActRespawn      Action = "respawn"
	ActAutopilotOn  Action = "autopilot_on"
	ActAutopilotOff Action = "autopilot_off"
)

// Header is the first line of a recording.
type Header struct {
	Kind    string `json:"k"`
	Format  string `json:"format"`
	Version int    `json:"v"`

	// Seed is the resolved seed, never the -seed flag: 0 there means
	// time-based, and a recording that stored the flag would be unreplayable.
	Seed     int64   `json:"seed"`
	TickRate float64 `json:"tick_rate"`

	// Population targets, which are mutable on the World and which the
	// Gymnasium environment server overrides after construction.
	Food    int `json:"food"`
	Bots    int `json:"bots"`
	Viruses int `json:"viruses"`

	ChecksumEvery int `json:"checksum_every"`

	// Provenance. Bit-exact replay is only promised for the same binary on the
	// same machine, so a player warns when any of these differs.
	GameVersion string `json:"game_version"`
	GOOS        string `json:"goos"`
	GOARCH      string `json:"goarch"`
	GoVersion   string `json:"go"`
	RecordedAt  string `json:"recorded_at"`
	// Window is provenance only — a replay is valid at any size, because the
	// recorded target is a world coordinate. A slice rather than an array so
	// omitempty works: headless recordings have no window at all.
	Window []int `json:"window,omitempty"`
}

// Frame is a run of consecutive ticks that shared a player target, plus the
// actions applied immediately before the first of them.
//
// One record per frame rather than per tick is lossless: the target is computed
// once per frame in the game loop and every tick of that frame is handed the
// same float64 pair.
type Frame struct {
	Kind string  `json:"k"`
	Tick int     `json:"t"`
	N    int     `json:"n"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`

	// Actions applied immediately before tick Tick, in order. A frame that ran
	// no ticks contributes its actions to the next frame that did, which is
	// correct because no Step intervenes.
	Actions []Action `json:"a,omitempty"`
}

// Checkpoint is a state hash written every ChecksumEvery ticks, and once at the
// end. The scalar witnesses travel with the hash so a divergence report can say
// what differs, not merely that something does.
type Checkpoint struct {
	Kind string `json:"k"`
	Tick int    `json:"t"`
	Hash string `json:"h"`
	Witness
}

// Witness is the human-readable half of a checkpoint. A virus count off by one
// says "a missed pop"; a blob count off by one says "a missed split".
type Witness struct {
	Time   float64 `json:"time"`
	Mass   float64 `json:"mass"`
	Blobs  int     `json:"blobs"`
	Food   int     `json:"food"`
	Ejecta int     `json:"ejecta"`
	Virus  int     `json:"virus"`
	Deaths int     `json:"deaths"`
	Best   float64 `json:"best,omitempty"`
}
