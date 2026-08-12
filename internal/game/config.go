package game

// All tunable constants live here. Every distance is in world units and every
// rate is per second — never per tick. The spec in description.md quotes speeds
// per tick at 60fps (Ks=2.2, Smin=1.0); those have been rescaled to units/second
// here. Mixing the two conventions produces a game 60x too slow.
const (
	// Arena.
	WorldSize = 4000.0

	// Simulation runs at a fixed timestep so Step is deterministic.
	TickRate = 120.0
	TickDT   = 1.0 / TickRate

	// Mass <-> radius: R = sqrt(M * MassRadiusScale). Mass 25 => radius 50.
	MassRadiusScale = 100.0

	// Speed: S = max(MinSpeed, SpeedBase / M^SpeedExp), in units/second.
	SpeedBase = 1300.0
	SpeedExp  = 0.44
	MinSpeed  = 45.0

	// Starting masses.
	PlayerStartMass = 25.0
	BotStartMass    = 20.0
	FoodMass        = 1.0
	EjectaMass      = 12.0

	// Split.
	SplitMinMass = 36.0
	MaxBlobs     = 16
	SplitImpulse = 700.0

	// Eject. The 4 mass lost between cost and pellet is the game's mass sink.
	EjectMinMass = 35.0
	EjectCost    = 16.0
	EjectImpulse = 780.0

	// Impulse velocity decays as v *= exp(-ImpulseDecay * dt).
	ImpulseDecay = 6.0

	// Recombine cooldown in seconds: T = MergeBase + M * MergeMassCoef.
	MergeBase     = 30.0
	MergeMassCoef = 0.02

	// Mass decay above DecayMinMass, as a fraction lost per second.
	DecayMinMass = 100.0
	DecayRate    = 0.002

	// Viruses.
	VirusMass       = 100.0
	VirusPopMinMass = 130.0
	VirusFeedCount  = 7
	VirusShotSpeed  = 600.0

	// Consumption rules (spec section 4).
	EatMassRatio   = 1.25
	EatOverlapCoef = 0.33
	MinBlobMass    = 10.0
	MaxBlobMassCap = 22500.0 // radius 1500, a quarter of the arena

	// Population targets.
	MaxFood    = 800
	MaxBots    = 15
	MaxViruses = 25

	// Spatial hash bucket size.
	GridCell = 200.0

	// Bot AI.
	BotThinkInterval = 0.15
	BotFOVCoef       = 9.0
	BotFOVMin        = 500.0
	BotFOVMax        = 2200.0
	BotThreatRatio   = 1.25
	BotPreyRatio     = 0.8
	BotSplitRatio    = 2.2
	BotWallMargin    = 300.0
	// BotFoodRange bounds the pellet search independently of the threat/prey
	// field of view. A big bot's FOV can span most of the arena, and scanning
	// every pellet in it each decision is both slow and pointless: it should be
	// eating what is nearby, not crossing the map for a distant pellet.
	BotFoodRange = 900.0
)

// FoodRadius is fixed and small; food is drawn as a pellet, not a mass-scaled disc.
const FoodRadius = 8.0

// EjectaRadius derives from EjectaMass like any other blob would.
var EjectaRadius = MassToRadius(EjectaMass) * 0.55

// VirusRadius is the collision radius of a virus.
var VirusRadius = MassToRadius(VirusMass)
