// Copyright (c) 2026 Christophe Pallier
// SPDX-License-Identifier: Apache-2.0

// Package agarienv serves the agario simulation to an external process over a
// line-based JSON protocol, so an RL agent written in another language can play.
//
// Wire format: one JSON object per line, in each direction, request and
// response paired by id. It is meant to be readable and to be driveable by
// hand — start the binary and type {"id":1,"cmd":"hello"}.
//
// The server reports facts and never a reward: the reward scheme, the
// termination rule and the observation tensor all live in the client, so they
// can be changed without rebuilding this binary.
package agarienv

// Protocol is the wire version. Bump it on any incompatible change; the client
// checks it during the handshake.
const Protocol = 1

// Error kinds. The client matches on these rather than on message text.
const (
	ErrBadJSON    = "bad_json"
	ErrUnknownCmd = "unknown_cmd"
	ErrNoSuchEnv  = "no_such_env"
	ErrBadAction  = "bad_action"
	ErrNotReset   = "not_reset"
	ErrBadBatch   = "bad_batch"
	ErrBadConfig  = "bad_config"
	ErrInternal   = "internal"
)

// Request is one command from the client.
type Request struct {
	ID  int    `json:"id"`
	Cmd string `json:"cmd"`

	EnvID  int     `json:"env_id"`
	Seed   *int64  `json:"seed,omitempty"`
	Config *Config `json:"config,omitempty"`

	// Action is [heading, trigger]. Heading indexes Headings compass
	// directions; trigger is 0 none, 1 split, 2 eject.
	Action *[2]int `json:"action,omitempty"`

	// Batch forms. Seeds and Actions are indexed by EnvIDs.
	EnvIDs  []int     `json:"env_ids,omitempty"`
	Seeds   []int64   `json:"seeds,omitempty"`
	Actions [][2]int  `json:"actions,omitempty"`
	Configs []*Config `json:"configs,omitempty"`
}

// Config overrides the world's population targets. A nil field keeps the
// default, so a client can send only what it wants to change.
type Config struct {
	Food    *int `json:"food,omitempty"`
	Bots    *int `json:"bots,omitempty"`
	Viruses *int `json:"viruses,omitempty"`
}

// Response is one answer. On success exactly one of Meta, State or States is
// set; they are nested rather than flattened into the envelope because Go's
// encoder cannot conditionally inline a nil embedded pointer.
type Response struct {
	ID int  `json:"id"`
	OK bool `json:"ok"`

	Kind  string `json:"kind,omitempty"`
	Error string `json:"error,omitempty"`

	Meta   *Meta   `json:"meta,omitempty"`
	State  *State  `json:"state,omitempty"`
	States []State `json:"states,omitempty"`
}

// Meta is the handshake. The client builds its observation and action spaces
// from these numbers rather than duplicating them as constants.
type Meta struct {
	Protocol int    `json:"protocol"`
	Game     string `json:"game"`

	WorldSize float64 `json:"world_size"`
	TickDT    float64 `json:"tick_dt"`
	Frames    int     `json:"frames"`     // simulation ticks per step command
	StepDT    float64 `json:"step_dt"`    // seconds of game time per step command
	MaxBlobs  int     `json:"max_blobs"`  // cap on the agent's own cells
	Headings  int     `json:"headings"`   // discrete compass directions
	Triggers  int     `json:"triggers"`   // none / split / eject
	ViewScale float64 `json:"view_scale"` // view radius = ViewScale * own radius

	KFood   int `json:"k_food"`
	KCells  int `json:"k_cells"`
	KVirus  int `json:"k_virus"`
	KEjecta int `json:"k_ejecta"`

	// Defaults the world was built with, so a client can report them.
	Food    int `json:"food"`
	Bots    int `json:"bots"`
	Viruses int `json:"viruses"`

	SplitMinMass float64 `json:"split_min_mass"`
	EjectMinMass float64 `json:"eject_min_mass"`
}

// State is what the client turns into an observation. Every array is padded to
// the K declared in the handshake, so its shape never depends on how many
// entities happen to be nearby.
//
// Positions in the neighbour arrays are relative to the agent's centre of mass
// and in world units; the client normalises them. Sorting is by distance,
// nearest first.
type State struct {
	EnvID int     `json:"env_id"`
	Tick  int     `json:"tick"`
	Time  float64 `json:"time"`

	Mass      float64      `json:"mass"`
	Radius    float64      `json:"radius"`
	SelfCount int          `json:"self_count"`
	Self      [][4]float64 `json:"self"` // dx, dy, mass, seconds until mergeable
	CX        float64      `json:"cx"`
	CY        float64      `json:"cy"`
	ViewR     float64      `json:"view_r"`

	Food    [][3]float64 `json:"food"`    // dx, dy, mass
	Cells   [][4]float64 `json:"cells"`   // dx, dy, mass, relation
	Viruses [][3]float64 `json:"viruses"` // dx, dy, mass
	Ejecta  [][3]float64 `json:"ejecta"`  // dx, dy, mass

	// Distance to each wall, so the agent can learn not to corner itself.
	WallLeft  float64 `json:"wall_left"`
	WallRight float64 `json:"wall_right"`
	WallUp    float64 `json:"wall_up"`
	WallDown  float64 `json:"wall_down"`

	CanSplit bool `json:"can_split"`
	CanEject bool `json:"can_eject"`
	Dead     bool `json:"dead"`
	Rank     int  `json:"rank"`
	NOwners  int  `json:"n_owners"`

	// Deltas for the step just taken, for reward shaping on the client.
	MassGain float64 `json:"mass_gain"` // exact: total mass now minus before
	// Kills is an approximation, not a fact the simulation records: enemy cells
	// that were within view before the step, no longer exist after it, and
	// coincided with the agent gaining mass. A cell eaten by a third party at
	// the same moment the agent was feeding will be miscounted. Use it for a
	// bonus term, not for scoring.
	Kills   int     `json:"kills"`
	TopMass float64 `json:"top_mass"` // leader's mass, for a relative score
}

// Relations reported in the Cells array, from the agent's point of view.
const (
	RelationThreat  = -1.0 // can eat at least one of the agent's cells
	RelationNeutral = 0.0
	RelationPrey    = 1.0 // the agent's largest cell can eat it
)
