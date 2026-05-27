package main

// =============================================================================
// Tile types
// =============================================================================

const (
	TileEmpty            = 0
	TileWall             = 1
	TilePellet           = 2
	TileDestructibleWall = 3
	TileCherry           = 4
	TileChest            = 5
)

// =============================================================================
// Player roles
// =============================================================================

const (
	RolePacman   = "PACMAN"
	RoleTracker  = "GHOST_TRACKER"
	RoleBuilder  = "GHOST_BUILDER"
	RoleSprinter = "GHOST_SPRINTER"
)

// =============================================================================
// Game states
// =============================================================================

const (
	StateLobby    = "LOBBY"
	StatePlaying  = "PLAYING"
	StateFinished = "FINISHED"
)

// =============================================================================
// Message types (client → server)
// =============================================================================

const (
	MsgTypeInput        = "INPUT"
	MsgTypeReady        = "READY"
	MsgTypeStartGame    = "START_GAME"
	MsgTypeUseInvis     = "USE_INVIS"
	MsgTypeSpectateNext = "SPECTATE_NEXT"
	MsgTypeBuild        = "BUILD"
	MsgTypeConfirmUDP   = "CONFIRM_UDP"
)

// =============================================================================
// Message types (server → client)
// =============================================================================

const (
	MsgTypeWelcome     = "WELCOME"
	MsgTypeLobbyUpdate = "LOBBY_UPDATE"
	MsgTypeGameStart   = "GAME_START"
	MsgTypeGameState   = "GAME_STATE"
	MsgTypeGameOver    = "GAME_OVER"
	MsgTypeError       = "ERROR"
)

// =============================================================================
// Game constants
// =============================================================================

const (
	MinPlayers   = 4
	MaxPlayers   = 12
	TicksPerSec  = 30
	PlayerRadius = 0.35
	StunSeconds  = 3
)

// Vision radii in tiles
const (
	VisionPacman   = 15.0
	VisionTracker  = 9.0
	VisionBuilder  = 6.0
	VisionSprinter = 12.0
)

// Speed in tiles per tick (30 Hz)
// Pacman is ~10% faster than average ghost
const (
	SpeedPacman   = 0.110 // ~3.3 tiles/sec
	SpeedTracker  = 0.080 // ~2.4 tiles/sec
	SpeedBuilder  = 0.078 // ~2.3 tiles/sec
	SpeedSprinter = 0.096 // ~2.9 tiles/sec
	SpeedDash     = 0.350 // ~10.5 tiles/sec during dash
)

// Cherry & Chest spawning
const (
	CherryFirstSpawnTicks  = 30 * TicksPerSec  // 30 seconds after game start
	CherryIntervalMinTicks = 15 * TicksPerSec  // min 15s between cherries
	CherryIntervalMaxTicks = 20 * TicksPerSec  // max 20s between cherries
	CherryPoints           = 50
)

const (
	ChestSpawnIntervalTicks = 28 * TicksPerSec // 28s between chests
)

// Invisibility
const (
	InvisMaxCharges  = 7
	InvisDurationSec = 30
)

// Ghost-vs-Ghost kill
const (
	GhostKillStunSec = 20
)

// Tracker directional indicator
const (
	TrackerIndicatorDurSec = 30
	TrackerIndicatorCDMs   = 60_000
)

// =============================================================================
// Payload structures: Client → Server
// =============================================================================

// IncomingMsg is the generic envelope for all client messages.
type IncomingMsg struct {
	Type   string  `json:"type"`
	Seq    int     `json:"seq"`
	DirX   float64 `json:"dir_x"`
	DirY   float64 `json:"dir_y"`
	Dash   bool    `json:"dash"`              // Use ability / sprint
	Coords []int   `json:"coords,omitempty"` // [x1, y1, x2, y2] for BUILD
}

// =============================================================================
// Payload structures: Server → Client
// =============================================================================

// ErrorPayload signals a server-side error to the client.
type ErrorPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type WelcomePayload struct {
	Type      string `json:"type"`
	YourID    string `json:"your_id"`
	RoomID    string `json:"room_id"`
	RoomState string `json:"room_state"`
	UDPPort   int    `json:"udp_port"`
}

// LobbyPlayer represents one player entry in the lobby list.
type LobbyPlayer struct {
	ID    string `json:"id"`
	Ready bool   `json:"ready"`
}

// LobbyUpdatePayload is broadcast whenever the lobby changes.
type LobbyUpdatePayload struct {
	Type       string        `json:"type"`
	Players    []LobbyPlayer `json:"players"`
	MinReady   int           `json:"min_ready"`
	MaxPlayers int           `json:"max_players"`
}

// GameStartPayload is sent once when the game begins.
type GameStartPayload struct {
	Type      string  `json:"type"`
	YourRole  string  `json:"your_role"`
	MapWidth  int     `json:"map_width"`
	MapHeight int     `json:"map_height"`
	SpawnX    float64 `json:"spawn_x"`
	SpawnY    float64 `json:"spawn_y"`
}

// EntityState represents any visible entity in the world payload.
type EntityState struct {
	ID           string  `json:"id"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	RevealedRole string  `json:"revealed_role,omitempty"`
}

// TileUpdate represents a single tile in the visibility window.
type TileUpdate struct {
	X int `json:"x"`
	Y int `json:"y"`
	T int `json:"t"` // tile type (TileEmpty / TileWall / TilePellet / TileDestructibleWall)
}

// PlayerStatus carries the local player's own status.
type PlayerStatus struct {
	Score           int     `json:"score"`
	Stunned         bool    `json:"stunned"`
	CooldownMs      int64   `json:"cooldown_ms"`
	Role            string  `json:"role"`
	Lives           int     `json:"lives"`
	InvisCharges    int     `json:"invis_charges"`
	IsInvisible     bool    `json:"is_invisible"`
	IsDead          bool    `json:"is_dead"`
	CherryDirAngle  float64 `json:"cherry_dir_angle"`  // radians, -999 = none
	TrackerDirAngle float64 `json:"tracker_dir_angle"` // radians, -999 = not active
	SpectatingID    string  `json:"spectating_id,omitempty"`
	IsDashing       bool    `json:"is_dashing"`
	DashRemainingTicks int `json:"dash_remaining_ticks"`
}

// GameStatePayload is the per-tick world snapshot sent to each player (30 Hz).
// The server applies culling before building this payload:
//   - only entities within the player's vision radius are included
//   - pellets are hidden from ghost players (TilePellet → TileEmpty)
//   - invisible players are excluded from everyone else's payload
//   - cherries and chests are only included for Pacman players
//   - the 'role' field of other players is omitted unless revealed
type GameStatePayload struct {
	Type    string        `json:"type"`
	Tick    int64         `json:"tick"`
	Players []EntityState `json:"players"`
	Tiles   []TileUpdate  `json:"tiles,omitempty"`
	Cherries []EntityState `json:"cherries,omitempty"`
	Chests   []EntityState `json:"chests,omitempty"`
	Status  PlayerStatus  `json:"status"`
	LastSeq int           `json:"last_seq"`
}

// GameOverPayload signals the end of the game with final scores.
type GameOverPayload struct {
	Type    string            `json:"type"`
	Winner  string            `json:"winner"`  // "PACMAN" or "GHOST"
	Scores  map[string]int    `json:"scores"`  // playerID → score
	Reveals map[string]string `json:"reveals"` // playerID → actual role
}

// RoomSummaryPayload is used by the /rooms HTTP endpoint.
type RoomSummaryPayload struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Players    int    `json:"players"`
	MaxPlayers int    `json:"max_players"`
}

// SpawnedItem represents a cherry or chest placed dynamically on the map.
type SpawnedItem struct {
	ID   string
	X, Y int
}

// UDPInput is sent by clients over UDP for fast inputs
type UDPInput struct {
	ClientID string  `json:"client_id"`
	Seq      int     `json:"seq"`
	DirX     float64 `json:"dir_x"`
	DirY     float64 `json:"dir_y"`
	Dash     bool    `json:"dash"`
}
