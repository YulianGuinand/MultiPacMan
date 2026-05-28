package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"
)

// =============================================================================
// Player — game entity tied to a Client connection
// =============================================================================

// Player holds all server-authoritative state for one connected player.
type Player struct {
	ID     string
	Client *Client

	// World position (tile coordinates, float for sub-tile precision)
	X, Y float64

	// Role & identity
	Role         string
	VisionRadius float64
	Speed        float64

	// Input state (set by the latest processed InputPayload)
	DirX, DirY float64
	LastSeq    int

	// Status
	IsStunned bool
	StunUntil time.Time
	Score     int

	// Player ability
	Ability      Ability
	AbilityReady time.Time // next time the ability can be used

	// Sprinter dash state
	IsDashing          bool
	DashUntil          time.Time
	DashRemainingTicks int
	DashDirX, DashDirY float64

	// Phaser state
	IsPhasing             bool
	PhasingRemainingTicks int

	// Lives (Pacman — granted by cherries)
	Lives int

	// Invisibility (Pacman)
	InvisCharges int
	IsInvisible  bool
	InvisUntil   time.Time

	// Death & spectation
	IsDead       bool
	SpectatingID string // ID of teammate being observed when dead

	// Delta tile tracking: last tiles sent to this player (for compression).
	// Key: (x,y) packed as int, value: tile type.
	LastSentTiles map[int]int

	// Lobby
	Ready bool
}

func (p *Player) IsGhost() bool {
	return RoleRegistry[p.Role].Group == "GHOST"
}

// =============================================================================
// Game — authoritative game room
// =============================================================================

// Game manages one room: lobby, map, players, and the 30 Hz tick loop.
type Game struct {
	ID string

	mu    sync.Mutex
	state string // StateLobby / StatePlaying / StateFinished

	clients map[string]*Client
	players map[string]*Player

	// Map
	grid      [][]int
	mapWidth  int
	mapHeight int

	// Pellet tracking
	totalPellets       int
	remainingPellets   int
	trapIndicatorUntil time.Time

	// Dynamically spawned items
	cherries       []SpawnedItem
	chests         []SpawnedItem
	nextCherryTick int64
	nextChestTick  int64
	itemIDCounter  int

	// Tick loop
	tick      int64
	ticker    *time.Ticker
	done      chan struct{}
	closeOnce sync.Once
	rng       *rand.Rand

	// Input queue: accumulate between ticks
	inputQueue map[string]IncomingMsg
}

func NewGame(id string) *Game {
	return &Game{
		ID:         id,
		state:      StateLobby,
		clients:    make(map[string]*Client),
		players:    make(map[string]*Player),
		done:       make(chan struct{}),
		inputQueue: make(map[string]IncomingMsg),
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Run is the goroutine launched by the Hub. It manages the 30 Hz tick loop until the room closes.
func (g *Game) Run() {
	g.ticker = time.NewTicker(time.Second / TicksPerSec)
	defer g.ticker.Stop()

	for {
		select {
		case <-g.done:
			log.Printf("[game %s] room closed", g.ID)
			return
		case <-g.ticker.C:
			g.processTick()
		}
	}
}

// closeDone closes the done channel exactly once (safe to call multiple times).
func (g *Game) closeDone() {
	g.closeOnce.Do(func() { close(g.done) })
}

// =============================================================================
// Room lifecycle
// =============================================================================

func (g *Game) GetState() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

func (g *Game) PlayerCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.clients)
}

func (g *Game) IsEmpty() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.clients) == 0
}

// AddClient registers a new client into the room.
// Returns false if the room is full.
func (g *Game) AddClient(c *Client) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.clients) >= MaxPlayers {
		return false
	}

	g.clients[c.ID] = c
	p := &Player{
		ID:            c.ID,
		Client:        c,
		LastSentTiles: make(map[int]int),
	}
	g.players[c.ID] = p

	c.SendJSON(WelcomePayload{
		Type:      MsgTypeWelcome,
		YourID:    c.ID,
		RoomID:    g.ID,
		RoomState: g.state,
		UDPPort:   GlobalUDPPort,
	})

	if g.state == StatePlaying {
		p.Role = RoleTracker
		p.IsDead = true
		p.SpectatingID = g.findFirstAliveGhostLocked()

		c.SendJSON(GameStartPayload{
			Type:      MsgTypeGameStart,
			YourRole:  p.Role,
			MapWidth:  g.mapWidth,
			MapHeight: g.mapHeight,
			SpawnX:    0.0,
			SpawnY:    0.0,
		})
	}

	if g.state != StatePlaying {
		g.broadcastLobbyUpdateLocked()
	}
	return true
}

// RemoveClient removes a client. If the room becomes empty it closes done.
func (g *Game) RemoveClient(c *Client) {
	g.mu.Lock()
	defer g.mu.Unlock()

	delete(g.clients, c.ID)
	delete(g.players, c.ID)

	if g.state == StateLobby {
		g.broadcastLobbyUpdateLocked()
	}

	if len(g.clients) == 0 {
		g.closeDone()
	}
}

// =============================================================================
// Message dispatch
// =============================================================================

// HandleMessage is called from the client's ReadPump goroutine.
func (g *Game) HandleMessage(clientID string, data []byte) {
	var msg IncomingMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		log.Printf("[game %s] unmarshal error from %s: %v", g.ID, clientID, err)
		return
	}

	switch msg.Type {
	case MsgTypeReady:
		g.handleReady(clientID)
	case MsgTypeStartGame:
		g.handleStartGame(clientID)
	case MsgTypeInput:
		g.handleInput(clientID, msg)
	case MsgTypeUseInvis:
		g.handleUseInvis(clientID)
	case MsgTypeSpectateNext:
		g.handleSpectateNext(clientID)
	case MsgTypeBuild:
		g.handleBuild(clientID, msg.Coords)
	case MsgTypeConfirmUDP:
		g.handleConfirmUDP(clientID)
	}
}

func (g *Game) handleConfirmUDP(clientID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.clients[clientID]; ok {
		c.UDPConfirmed = true
		log.Printf("[game %s] Client %s UDP connection confirmed", g.ID, clientID)
	}
}

func (g *Game) handleReady(clientID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.players[clientID]
	if !ok {
		return
	}

	if g.state == StateFinished {
		g.resetToLobbyLocked()
		return
	}

	if g.state != StateLobby {
		return
	}
	p.Ready = true
	g.broadcastLobbyUpdateLocked()

	// Auto-start when all connected players (≥ MinPlayers) are ready.
	if len(g.players) >= MinPlayers {
		allReady := true
		for _, pl := range g.players {
			if !pl.Ready {
				allReady = false
				break
			}
		}
		if allReady {
			g.startGameLocked()
		}
	}
}

func (g *Game) handleStartGame(clientID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.state != StateLobby {
		return
	}
	readyCount := 0
	for _, p := range g.players {
		if p.Ready {
			readyCount++
		}
	}
	if readyCount < MinPlayers {
		c, ok := g.clients[clientID]
		if ok {
			c.SendJSON(ErrorPayload{
				Type:    MsgTypeError,
				Message: "Not enough ready players (min 4)",
			})
		}
		return
	}
	g.startGameLocked()
}

func (g *Game) handleInput(clientID string, msg IncomingMsg) {
	if g.state != StatePlaying {
		return
	}
	g.mu.Lock()
	// Only keep the latest input per player per tick.
	existing, ok := g.inputQueue[clientID]
	if !ok || msg.Seq > existing.Seq {
		g.inputQueue[clientID] = msg
	}
	g.mu.Unlock()
}

func (g *Game) handleUseInvis(clientID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.players[clientID]
	if !ok || p.Role != RolePacman || p.IsDead || p.InvisCharges <= 0 || p.IsInvisible {
		return
	}
	p.InvisCharges--
	p.IsInvisible = true
	p.InvisUntil = time.Now().Add(InvisDurationSec * time.Second)
	log.Printf("[game %s] %s activated invisibility (%d charges left)", g.ID, clientID, p.InvisCharges)
}

func (g *Game) handleSpectateNext(clientID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.players[clientID]
	if !ok || !p.IsDead {
		return
	}
	p.SpectatingID = g.findNextAliveTeammateLocked(p)
}

// =============================================================================
// Game start
// =============================================================================

func (g *Game) startGameLocked() {
	n := len(g.players)
	g.mapWidth, g.mapHeight = calcMapSize(n)
	g.grid = GenerateMap(g.mapWidth, g.mapHeight)

	// Count pellets.
	g.totalPellets = 0
	for y := 0; y < g.mapHeight; y++ {
		for x := 0; x < g.mapWidth; x++ {
			if g.grid[y][x] == TilePellet {
				g.totalPellets++
			}
		}
	}
	g.remainingPellets = g.totalPellets

	// Assign roles and spawn positions.
	AssignRoles(g.players, g.grid, g.mapWidth, g.mapHeight)

	// Schedule first cherry and chest spawns.
	g.nextCherryTick = CherryFirstSpawnTicks
	g.nextChestTick = ChestSpawnIntervalTicks

	// Spawn an initial cherry immediately at game start
	g.spawnCherryLocked()

	g.state = StatePlaying

	// Notify each player of their role and spawn position.
	for _, p := range g.players {
		p.Client.SendJSON(GameStartPayload{
			Type:      MsgTypeGameStart,
			YourRole:  p.Role,
			MapWidth:  g.mapWidth,
			MapHeight: g.mapHeight,
			SpawnX:    p.X,
			SpawnY:    p.Y,
		})
	}

	log.Printf("[game %s] started — %d players, map %dx%d, %d pellets",
		g.ID, n, g.mapWidth, g.mapHeight, g.totalPellets)

}

// =============================================================================
// Tick loop
// =============================================================================

func (g *Game) processTick() {
	g.mu.Lock()

	if g.state != StatePlaying {
		g.mu.Unlock()
		return
	}

	now := time.Now()
	g.tick++

	// --- 1. Process accumulated inputs ---
	for clientID, input := range g.inputQueue {
		p, ok := g.players[clientID]
		if !ok || p.IsDead {
			continue
		}
		if input.Seq > p.LastSeq {
			p.LastSeq = input.Seq
			p.DirX = input.DirX
			p.DirY = input.DirY

			// Activate ability.
			if input.Dash && p.Ability != nil && p.Ability.IsSpacebarTriggered() && p.Ability.IsReady(now) {
				p.Ability.UseAbility(g, p)
				// Set AbilityReady so the cooldown bar updates immediately.
				p.AbilityReady = now.Add(time.Duration(p.Ability.GetCooldownMs()) * time.Millisecond)
			}
		}
	}
	g.inputQueue = make(map[string]IncomingMsg)

	// --- 2. Update positions ---
	for _, p := range g.players {
		if p.IsDead {
			continue
		}

		// Clear expired stun.
		if p.IsStunned && now.After(p.StunUntil) {
			p.IsStunned = false
		}
		if p.IsStunned {
			continue
		}

		speed := p.Speed
		dirX, dirY := p.DirX, p.DirY

		// Dash overrides speed and direction.
		if p.IsDashing {
			if p.DashRemainingTicks <= 0 {
				p.IsDashing = false
			} else {
				p.DashRemainingTicks--
				speed = SpeedDash
				dirX = p.DashDirX
				dirY = p.DashDirY
			}
		}

		// Phaser overrides collision.
		if p.IsPhasing {
			if p.PhasingRemainingTicks <= 0 {
				p.IsPhasing = false
				// Check if inside wall
				tx := int(math.Floor(p.X))
				ty := int(math.Floor(p.Y))
				if tx >= 0 && tx < g.mapWidth && ty >= 0 && ty < g.mapHeight {
					if g.grid[ty][tx] == TileWall || g.grid[ty][tx] == TileDestructibleWall {
						p.IsDead = true
						p.SpectatingID = g.findFirstAliveTeammateLocked(p)
						log.Printf("[game %s] phaser %s died inside a wall!", g.ID, p.ID)
						continue
					}
				}
			} else {
				p.PhasingRemainingTicks--
			}
		}

		newX := p.X + dirX*speed
		newY := p.Y + dirY*speed

		// Phaser can pass through normal walls but not builder walls
		canMove := false
		if p.IsPhasing {
			// Check if destination has a destructible wall
			txMin, txMax := int(math.Floor(newX-PlayerRadius)), int(math.Floor(newX+PlayerRadius))
			tyMin, tyMax := int(math.Floor(newY-PlayerRadius)), int(math.Floor(newY+PlayerRadius))
			blockedByBuilder := false
			for ty := tyMin; ty <= tyMax; ty++ {
				for tx := txMin; tx <= txMax; tx++ {
					if tx >= 0 && tx < g.mapWidth && ty >= 0 && ty < g.mapHeight {
						if g.grid[ty][tx] == TileDestructibleWall {
							blockedByBuilder = true
							break
						}
					}
				}
				if blockedByBuilder {
					break
				}
			}
			canMove = !blockedByBuilder
		} else {
			canMove = !g.wouldCollide(newX, newY)
		}

		if canMove {
			p.X = newX
			p.Y = newY
		} else {
			// Slide logic: try moving horizontally or vertically independently
			if !g.wouldCollide(newX, p.Y) {
				p.X = newX
			} else if !g.wouldCollide(p.X, newY) {
				p.Y = newY
			} else {
				// Completely blocked
				if p.IsDashing && p.Role == RoleSprinter {
					p.IsDashing = false
					p.DashRemainingTicks = 0
					p.IsStunned = true
					p.StunUntil = now.Add(StunSeconds * time.Second)
				}
			}
		}

		// Clamp to map bounds.
		p.X = clamp(p.X, PlayerRadius, float64(g.mapWidth)-1-PlayerRadius)
		p.Y = clamp(p.Y, PlayerRadius, float64(g.mapHeight)-1-PlayerRadius)

		// Round to 3 decimal places for physical determinism
		p.X = math.Round(p.X*1000.0) / 1000.0
		p.Y = math.Round(p.Y*1000.0) / 1000.0

	}

	// --- 3. Invisibility expiration ---
	for _, p := range g.players {
		if p.IsInvisible && now.After(p.InvisUntil) {
			p.IsInvisible = false
			log.Printf("[game %s] %s invisibility expired", g.ID, p.ID)
		}
	}

	// --- 4. Pellet collection (Pacmans only) ---
	for _, p := range g.players {
		if RoleRegistry[p.Role].Group != "PACMAN" || p.IsDead || p.IsStunned {
			continue
		}
		xMin := int(math.Floor(p.X - PlayerRadius))
		xMax := int(math.Floor(p.X + PlayerRadius))
		yMin := int(math.Floor(p.Y - PlayerRadius))
		yMax := int(math.Floor(p.Y + PlayerRadius))

		for ty := yMin; ty <= yMax; ty++ {
			for tx := xMin; tx <= xMax; tx++ {
				if tx >= 0 && tx < g.mapWidth && ty >= 0 && ty < g.mapHeight {
					if g.grid[ty][tx] == TilePellet {
						g.grid[ty][tx] = TileEmpty
						p.Score += 10
						g.remainingPellets--
					} else if g.grid[ty][tx] == TileFakePellet {
						g.grid[ty][tx] = TileEmpty
						g.trapIndicatorUntil = now.Add(3 * time.Second)
						log.Printf("[game %s] %s triggered a trap! Indicator active.", g.ID, p.ID)
					}
				}
			}
		}
	}

	// --- 5. Cherry spawning ---
	if g.tick >= g.nextCherryTick {
		if len(g.cherries) < 5 {
			g.spawnCherryLocked()
		}
		interval := CherryIntervalMinTicks + g.rng.Intn(CherryIntervalMaxTicks-CherryIntervalMinTicks+1)
		g.nextCherryTick = g.tick + int64(interval)
	}

	// --- 6. Cherry collection (Pacmans only) ---
	for _, p := range g.players {
		if RoleRegistry[p.Role].Group != "PACMAN" || p.IsDead || p.IsStunned {
			continue
		}
		xMin := int(math.Floor(p.X - PlayerRadius))
		xMax := int(math.Floor(p.X + PlayerRadius))
		yMin := int(math.Floor(p.Y - PlayerRadius))
		yMax := int(math.Floor(p.Y + PlayerRadius))

		remaining := g.cherries[:0]
		for _, c := range g.cherries {
			collected := false
			for ty := yMin; ty <= yMax; ty++ {
				for tx := xMin; tx <= xMax; tx++ {
					if c.X == tx && c.Y == ty {
						collected = true
						break
					}
				}
				if collected {
					break
				}
			}
			if collected {
				p.Score += CherryPoints
				p.Lives++
				log.Printf("[game %s] %s collected cherry (+%d pts, lives=%d)", g.ID, p.ID, CherryPoints, p.Lives)
			} else {
				remaining = append(remaining, c)
			}
		}
		g.cherries = remaining
	}

	// --- 7. Chest spawning ---
	if g.tick >= g.nextChestTick {
		g.spawnChestLocked()
		g.nextChestTick = g.tick + int64(ChestSpawnIntervalTicks)
	}

	// --- 8. Chest collection (Pacmans only) ---
	for _, p := range g.players {
		if RoleRegistry[p.Role].Group != "PACMAN" || p.IsDead || p.IsStunned {
			continue
		}
		xMin := int(math.Floor(p.X - PlayerRadius))
		xMax := int(math.Floor(p.X + PlayerRadius))
		yMin := int(math.Floor(p.Y - PlayerRadius))
		yMax := int(math.Floor(p.Y + PlayerRadius))

		remaining := g.chests[:0]
		for _, ch := range g.chests {
			collected := false
			for ty := yMin; ty <= yMax; ty++ {
				for tx := xMin; tx <= xMax; tx++ {
					if ch.X == tx && ch.Y == ty {
						collected = true
						break
					}
				}
				if collected {
					break
				}
			}
			if collected {
				if p.InvisCharges < InvisMaxCharges {
					p.InvisCharges++
					log.Printf("[game %s] %s collected chest (invis charges=%d)", g.ID, p.ID, p.InvisCharges)
				}
			} else {
				remaining = append(remaining, ch)
			}
		}
		g.chests = remaining
	}

	// --- 9. Ghost-Pacman collisions ---
	for _, ghost := range g.players {
		if !ghost.IsGhost() || ghost.IsStunned || ghost.IsDead {
			continue
		}
		for _, pacman := range g.players {
			if RoleRegistry[pacman.Role].Group != "PACMAN" || pacman.IsDead || pacman.IsStunned {
				continue
			}
			if dist2D(ghost.X, ghost.Y, pacman.X, pacman.Y) < PlayerRadius*2 {
				if pacman.Lives > 0 {
					// Pacman has extra lives — respawn
					pacman.Lives--
					spawnPt := g.findRandomFloorTileLocked()
					pacman.X = float64(spawnPt[0]) + 0.5
					pacman.Y = float64(spawnPt[1]) + 0.5
					ghost.Score += 100
					log.Printf("[game %s] %s caught %s (respawn, lives=%d)", g.ID, ghost.ID, pacman.ID, pacman.Lives)
				} else {
					// Pacman dies
					pacman.IsDead = true
					pacman.SpectatingID = g.findFirstAliveTeammateLocked(pacman)
					ghost.Score += 200
					log.Printf("[game %s] %s killed %s (pacman dead)", g.ID, ghost.ID, pacman.ID)
				}
			}
		}
	}

	// --- 10. Ghost-Ghost collisions ---
	ghostList := make([]*Player, 0)
	for _, p := range g.players {
		if p.IsGhost() && !p.IsDead && !p.IsStunned {
			ghostList = append(ghostList, p)
		}
	}
	for i := 0; i < len(ghostList); i++ {
		for j := i + 1; j < len(ghostList); j++ {
			g1, g2 := ghostList[i], ghostList[j]
			if dist2D(g1.X, g1.Y, g2.X, g2.Y) < PlayerRadius*2 {
				// Determine victim (slower) and attacker (faster)
				victim, attacker := g1, g2
				if g1.Speed > g2.Speed {
					victim, attacker = g2, g1
				} else if g1.Speed == g2.Speed {
					// Random if same speed
					if g.rng.Intn(2) == 0 {
						victim, attacker = g2, g1
					}
				}
				victim.IsDead = true
				victim.SpectatingID = g.findFirstAliveTeammateLocked(victim)
				attacker.IsStunned = true
				attacker.StunUntil = now.Add(GhostKillStunSec * time.Second)
				log.Printf("[game %s] ghost %s killed ghost %s (attacker stunned %ds)",
					g.ID, attacker.ID, victim.ID, GhostKillStunSec)
			}
		}
	}

	// --- 11. Win conditions ---
	// Pacmans win if all pellets are collected.
	if g.remainingPellets <= 0 {
		g.endGameLocked("PACMAN")
		g.mu.Unlock()
		return
	}

	// Pacmans win if any Pacman reaches 3000 points.
	for _, p := range g.players {
		if RoleRegistry[p.Role].Group == "PACMAN" && p.Score >= 3000 {
			g.endGameLocked("PACMAN")
			g.mu.Unlock()
			return
		}
	}

	// Ghosts win if all Pacmans are dead.
	pacmanAlive := false
	for _, p := range g.players {
		if RoleRegistry[p.Role].Group == "PACMAN" && !p.IsDead {
			pacmanAlive = true
			break
		}
	}
	if !pacmanAlive {
		g.endGameLocked("GHOST")
		g.mu.Unlock()
		return
	}

	// --- 12. Broadcast per-player state (outside of lock, parallelized) ---
	payloads := make([]playerPayload, 0, len(g.players))
	for _, p := range g.players {
		payloads = append(payloads, playerPayload{
			player:  p,
			payload: g.buildStateForLocked(p, now),
		})
	}
	g.mu.Unlock()

	g.broadcastGameStateParallel(payloads)
}

type playerPayload struct {
	player  *Player
	payload GameStatePayload
}

// =============================================================================
// Item spawning
// =============================================================================

func (g *Game) nextItemID() string {
	g.itemIDCounter++
	return fmt.Sprintf("item_%d", g.itemIDCounter)
}

func (g *Game) spawnCherryLocked() {
	pt := g.findRandomFloorTileLocked()
	cherry := SpawnedItem{ID: g.nextItemID(), X: pt[0], Y: pt[1]}
	g.cherries = append(g.cherries, cherry)
	log.Printf("[game %s] cherry spawned at (%d, %d)", g.ID, pt[0], pt[1])
}

func (g *Game) spawnChestLocked() {
	pt := g.findRandomFloorTileLocked()
	chest := SpawnedItem{ID: g.nextItemID(), X: pt[0], Y: pt[1]}
	g.chests = append(g.chests, chest)
	log.Printf("[game %s] chest spawned at (%d, %d)", g.ID, pt[0], pt[1])
}

func (g *Game) placeFakePellet(x, y int) {
	if x >= 0 && x < g.mapWidth && y >= 0 && y < g.mapHeight {
		if g.grid[y][x] == TileEmpty {
			g.grid[y][x] = TileFakePellet
			log.Printf("[game %s] fake pellet placed at (%d, %d)", g.ID, x, y)
		}
	}
}

// findRandomFloorTileLocked returns a random walkable (empty or pellet) tile.
func (g *Game) findRandomFloorTileLocked() [2]int {
	// Collect walkable tiles.
	var walkable [][2]int
	for y := 1; y < g.mapHeight-1; y++ {
		for x := 1; x < g.mapWidth-1; x++ {
			t := g.grid[y][x]
			if t == TileEmpty || t == TilePellet {
				walkable = append(walkable, [2]int{x, y})
			}
		}
	}
	if len(walkable) == 0 {
		return [2]int{g.mapWidth / 2, g.mapHeight / 2}
	}
	return walkable[g.rng.Intn(len(walkable))]
}

// =============================================================================
// Teammate finding (for spectation)
// =============================================================================

func (g *Game) findFirstAliveGhostLocked() string {
	for _, other := range g.players {
		if other.IsGhost() && !other.IsDead {
			return other.ID
		}
	}
	// Fallback to any ghost if none are alive
	for _, other := range g.players {
		if other.IsGhost() {
			return other.ID
		}
	}
	return ""
}

// findFirstAliveTeammateLocked returns the ID of any alive teammate (same team).
func (g *Game) findFirstAliveTeammateLocked(p *Player) string {
	isGhost := p.IsGhost()
	for _, other := range g.players {
		if other.ID == p.ID || other.IsDead {
			continue
		}
		if isGhost && other.IsGhost() {
			return other.ID
		}
		if !isGhost && !other.IsGhost() {
			return other.ID
		}
	}
	return "" // no alive teammate
}

// findNextAliveTeammateLocked cycles to the next alive teammate after the current spectating target.
func (g *Game) findNextAliveTeammateLocked(p *Player) string {
	isGhost := p.IsGhost()
	// Collect alive teammates.
	var teammates []*Player
	for _, other := range g.players {
		if other.ID == p.ID || other.IsDead {
			continue
		}
		if isGhost && other.IsGhost() {
			teammates = append(teammates, other)
		}
		if !isGhost && !other.IsGhost() {
			teammates = append(teammates, other)
		}
	}
	if len(teammates) == 0 {
		return ""
	}
	// Find current index and advance.
	currentIdx := -1
	for i, t := range teammates {
		if t.ID == p.SpectatingID {
			currentIdx = i
			break
		}
	}
	nextIdx := (currentIdx + 1) % len(teammates)
	return teammates[nextIdx].ID
}

// =============================================================================
// Collision helpers
// =============================================================================

// wouldCollide returns true if placing the player centre at (x, y) would
// overlap any solid tile (wall or destructible wall).
func (g *Game) wouldCollide(x, y float64) bool {
	r := PlayerRadius
	corners := [4][2]float64{
		{x - r, y - r},
		{x + r, y - r},
		{x - r, y + r},
		{x + r, y + r},
	}
	for _, c := range corners {
		tx := int(math.Floor(c[0]))
		ty := int(math.Floor(c[1]))
		if tx < 0 || tx >= g.mapWidth || ty < 0 || ty >= g.mapHeight {
			return true
		}
		t := g.grid[ty][tx]
		if t == TileWall || t == TileDestructibleWall {
			return true
		}
	}
	return false
}

func dist2D(x1, y1, x2, y2 float64) float64 {
	dx, dy := x1-x2, y1-y2
	return math.Sqrt(dx*dx + dy*dy)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// =============================================================================
// State broadcast — per-player culling
// =============================================================================

func (g *Game) broadcastGameStateParallel(payloads []playerPayload) {
	var wg sync.WaitGroup
	for _, pp := range payloads {
		wg.Add(1)
		go func(p *Player, payload GameStatePayload) {
			defer wg.Done()

			sentTCP := false
			if len(payload.Tiles) > 0 {
				p.Client.SendJSON(payload)
				sentTCP = true
				// Clear tiles so they aren't sent over UDP (saves bandwidth, prevents fragmentation)
				payload.Tiles = nil
			}

			sentUDP := false
			if p.Client.UDPAddr != nil && p.Client.hub.UDPConn != nil {
				data, err := json.Marshal(payload)
				if err == nil {
					_, err = p.Client.hub.UDPConn.WriteToUDP(data, p.Client.UDPAddr)
					if err == nil {
						sentUDP = true
					}
				}
			}

			// Only send over TCP if we haven't already sent this tick's payload over TCP,
			// and UDP is not yet confirmed/active or UDP transmission failed.
			if !sentTCP && (!p.Client.UDPConfirmed || !sentUDP) {
				p.Client.SendJSON(payload)
			}
		}(pp.player, pp.payload)
	}
	wg.Wait()
}

func (g *Game) buildStateForLocked(p *Player, now time.Time) GameStatePayload {
	var entities []EntityState
	var tiles []TileUpdate
	var visibleCherries []EntityState
	var visibleChests []EntityState

	isPacman := RoleRegistry[p.Role].Group == "PACMAN"
	isSpectatedPacman := false

	// Determine the viewpoint: if dead and spectating, use spectated player's position.
	viewX, viewY := p.X, p.Y
	viewRadius := p.VisionRadius
	spectatedPlayer := p
	if p.IsDead && p.SpectatingID != "" {
		if sp, ok := g.players[p.SpectatingID]; ok && !sp.IsDead {
			viewX, viewY = sp.X, sp.Y
			viewRadius = sp.VisionRadius
			spectatedPlayer = sp
			isSpectatedPacman = RoleRegistry[spectatedPlayer.Role].Group == "PACMAN"
		}
	}

	// --- Visible entities ---
	for _, other := range g.players {
		if other.IsDead {
			continue
		}
		if dist2D(viewX, viewY, other.X, other.Y) > viewRadius {
			continue
		}
		// Invisibility: invisible players are hidden from everyone except themselves.
		if other.IsInvisible && other.ID != p.ID {
			continue
		}
		// Phasing: players phasing through walls are hidden from others except themselves.
		if other.IsPhasing && other.ID != p.ID {
			tx := int(math.Floor(other.X))
			ty := int(math.Floor(other.Y))
			if tx >= 0 && tx < g.mapWidth && ty >= 0 && ty < g.mapHeight {
				if g.grid[ty][tx] == TileWall || g.grid[ty][tx] == TileDestructibleWall {
					continue
				}
			}
		}
		e := EntityState{
			ID: other.ID,
			X:  other.X,
			Y:  other.Y,
			// Don't send IsPhasing or PhasingRemainingTicks to other players — hide ability state
		}
		entities = append(entities, e)
	}

	// --- Visible tiles — DELTA COMPRESSION ---
	// Only send tiles that changed since the last time we sent them to this player.
	// Full resync every 30 ticks (1 second) to correct any drift.
	fullSync := (g.tick % 30) == 0
	r := int(viewRadius) + 8
	vrSq := (viewRadius + 8.0) * (viewRadius + 8.0) // send a 8-tile pre-cache buffer to prevent client lag
	px, py := int(math.Round(viewX)), int(math.Round(viewY))

	// Track which tiles are currently visible (to prune stale cache entries).
	visibleKeys := make(map[int]struct{}, (2*r+1)*(2*r+1))

	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if float64(dx*dx+dy*dy) > vrSq {
				continue
			}
			tx, ty := px+dx, py+dy
			if tx < 0 || tx >= g.mapWidth || ty < 0 || ty >= g.mapHeight {
				continue
			}
			tileType := g.grid[ty][tx]
			// Anti-cheat: Pacmans see TileFakePellet as TilePellet (2). Ghosts see TileFakePellet as TileEmpty (0).
			if tileType == TileFakePellet {
				if isPacman {
					tileType = TilePellet
				} else {
					tileType = TileEmpty
				}
			} else {
				// Anti-cheat: ghosts see pellet tiles as empty.
				if tileType == TilePellet && !isPacman {
					tileType = TileEmpty
				}
				// Anti-cheat: ghosts cannot see cherry/chest tiles. Builder also cannot see chests.
				if tileType == TileCherry && !isPacman {
					tileType = TileEmpty
				}
				if tileType == TileChest && (!isPacman || p.Role == RoleBuilder) {
					tileType = TileEmpty
				}
			}

			key := ty*g.mapWidth + tx
			visibleKeys[key] = struct{}{}

			// Delta: only send if changed or full sync.
			prev, known := p.LastSentTiles[key]
			if fullSync || !known || prev != tileType {
				tiles = append(tiles, TileUpdate{X: tx, Y: ty, T: tileType})
				p.LastSentTiles[key] = tileType
			}
		}
	}

	// Prune tiles that left the vision radius (so re-entering triggers a resend).
	for key := range p.LastSentTiles {
		if _, still := visibleKeys[key]; !still {
			delete(p.LastSentTiles, key)
		}
	}

	// --- Cherries (Pacman only) ---
	if isPacman || (p.IsDead && isSpectatedPacman) {
		for _, c := range g.cherries {
			cx, cy := float64(c.X)+0.5, float64(c.Y)+0.5
			if dist2D(viewX, viewY, cx, cy) <= viewRadius {
				visibleCherries = append(visibleCherries, EntityState{
					ID: c.ID, X: cx, Y: cy,
				})
			}
		}
	}

	// --- Chests (Pacman only, except Builder) ---
	isBuilderSpectator := p.IsDead && spectatedPlayer.Role == RoleBuilder
	if (isPacman || (p.IsDead && isSpectatedPacman)) && p.Role != RoleBuilder && !isBuilderSpectator {
		for _, ch := range g.chests {
			cx, cy := float64(ch.X)+0.5, float64(ch.Y)+0.5
			if dist2D(viewX, viewY, cx, cy) <= viewRadius {
				visibleChests = append(visibleChests, EntityState{
					ID: ch.ID, X: cx, Y: cy,
				})
			}
		}
	}

	// --- Cherry directional indicator (Pacman only) ---
	cherryAngle := -999.0
	if isPacman && !p.IsDead && len(g.cherries) > 0 {
		bestDist := math.MaxFloat64
		for _, c := range g.cherries {
			cx, cy := float64(c.X)+0.5, float64(c.Y)+0.5
			d := dist2D(p.X, p.Y, cx, cy)
			if d < bestDist {
				bestDist = d
				cherryAngle = math.Atan2(cy-p.Y, cx-p.X)
			}
		}
	}

	// --- Tracker / Trapper directional indicator ---
	trackerAngle := -999.0
	isTrapActive := now.Before(g.trapIndicatorUntil)
	isTrackerActive := false
	if p.Role == RoleTracker && p.Ability != nil {
		if ta, ok := p.Ability.(*TrackerAbility); ok {
			isTrackerActive = ta.IsIndicatorActive(now)
		}
	}

	if !p.IsDead && ((p.IsGhost() && isTrapActive) || isTrackerActive) {
		bestDist := math.MaxFloat64
		for _, other := range g.players {
			if RoleRegistry[other.Role].Group != "PACMAN" || other.IsDead {
				continue
			}
			// Indicator IGNORES invisibility
			d := dist2D(p.X, p.Y, other.X, other.Y)
			if d < bestDist {
				bestDist = d
				trackerAngle = math.Atan2(other.Y-p.Y, other.X-p.X)
			}
		}
	}

	// --- Cooldown ---
	var cooldownMs int64
	if p.Ability != nil {
		if remaining := p.AbilityReady.Sub(now); remaining > 0 {
			cooldownMs = remaining.Milliseconds()
		}
	}

	return GameStatePayload{
		Type:     MsgTypeGameState,
		Tick:     g.tick,
		Players:  entities,
		Tiles:    tiles,
		Cherries: visibleCherries,
		Chests:   visibleChests,
		Status: PlayerStatus{
			Score:                 p.Score,
			Stunned:               p.IsStunned,
			CooldownMs:            cooldownMs,
			Role:                  p.Role,
			Lives:                 p.Lives,
			InvisCharges:          p.InvisCharges,
			IsInvisible:           p.IsInvisible,
			IsDead:                p.IsDead,
			CherryDirAngle:        cherryAngle,
			TrackerDirAngle:       trackerAngle,
			SpectatingID:          p.SpectatingID,
			IsDashing:             p.IsDashing,
			DashRemainingTicks:    p.DashRemainingTicks,
			IsPhasing:             p.IsPhasing,
			PhasingRemainingTicks: p.PhasingRemainingTicks,
		},
		LastSeq: p.LastSeq,
	}
}

// =============================================================================
// Lobby broadcast
// =============================================================================

func (g *Game) broadcastLobbyUpdateLocked() {
	players := make([]LobbyPlayer, 0, len(g.players))
	for _, p := range g.players {
		players = append(players, LobbyPlayer{ID: p.ID, Ready: p.Ready})
	}
	payload := LobbyUpdatePayload{
		Type:       MsgTypeLobbyUpdate,
		Players:    players,
		MinReady:   MinPlayers,
		MaxPlayers: MaxPlayers,
	}
	for _, c := range g.clients {
		c.SendJSON(payload)
	}
}

// =============================================================================
// Game over
// =============================================================================

func (g *Game) endGameLocked(winner string) {
	g.state = StateFinished
	scores := make(map[string]int, len(g.players))
	reveals := make(map[string]string, len(g.players))
	for _, p := range g.players {
		scores[p.ID] = p.Score
		reveals[p.ID] = p.Role
	}
	payload := GameOverPayload{
		Type:    MsgTypeGameOver,
		Winner:  winner,
		Scores:  scores,
		Reveals: reveals,
	}
	for _, c := range g.clients {
		c.SendJSON(payload)
	}
	log.Printf("[game %s] game over — winner: %s", g.ID, winner)
}

func (g *Game) resetToLobbyLocked() {
	g.state = StateLobby
	g.tick = 0
	g.cherries = nil
	g.chests = nil
	g.inputQueue = make(map[string]IncomingMsg)

	for _, p := range g.players {
		p.Ready = false
		p.IsDead = false
		p.IsStunned = false
		p.Score = 0
		p.Lives = 0
		p.IsInvisible = false
		p.SpectatingID = ""
		p.LastSentTiles = make(map[int]int)
	}

	g.broadcastLobbyUpdateLocked()
}

func (g *Game) handleBuild(clientID string, coords []int) {
	if g.GetState() != StatePlaying {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.players[clientID]
	if !ok || p.IsDead || p.Role != RoleBuilder || p.IsStunned {
		return
	}

	now := time.Now()
	if p.Ability == nil || !p.Ability.IsReady(now) {
		return
	}

	if len(coords) != 4 {
		return
	}

	x1, y1, x2, y2 := coords[0], coords[1], coords[2], coords[3]

	// 1. Vérification des limites de la map
	if x1 < 0 || x1 >= g.mapWidth || y1 < 0 || y1 >= g.mapHeight ||
		x2 < 0 || x2 >= g.mapWidth || y2 < 0 || y2 >= g.mapHeight {
		return // Hors limites
	}

	// 2. Vérification de l'adjacence
	dx := abs(x1 - x2)
	dy := abs(y1 - y2)
	if (dx + dy) != 1 {
		return // Blocs non adjacents
	}

	// 3. Vérification de la disponibilité :
	// Le premier bloc doit être TileEmpty ou TilePellet. Le second peut être TileEmpty, TilePellet ou TileWall.
	if (g.grid[y1][x1] != TileEmpty && g.grid[y1][x1] != TilePellet) ||
		(g.grid[y2][x2] != TileEmpty && g.grid[y2][x2] != TilePellet && g.grid[y2][x2] != TileWall) {
		return // Zone occupée (autre qu'un mur permanent, vide, ou pellet)
	}

	// 4. Application
	if g.grid[y1][x1] == TilePellet {
		g.remainingPellets--
	}
	g.grid[y1][x1] = TileDestructibleWall

	// On ne remplace pas un mur permanent par un mur destructible
	if g.grid[y2][x2] == TileEmpty || g.grid[y2][x2] == TilePellet {
		if g.grid[y2][x2] == TilePellet {
			g.remainingPellets--
		}
		g.grid[y2][x2] = TileDestructibleWall
	}

	// 5. Déclenchement du cooldown
	if p.Ability != nil {
		p.Ability.SetUsed(now)
	}
	p.AbilityReady = now.Add(time.Duration(p.Ability.GetCooldownMs()) * time.Millisecond)

	// 6. Schedule auto-destruction after 15 seconds
	done := g.done
	wallsCopy := [2][2]int{{x1, y1}, {x2, y2}}
	go func() {
		select {
		case <-done:
			return
		case <-time.After(15 * time.Second):
		}
		g.mu.Lock()
		for _, wt := range wallsCopy {
			tx, ty := wt[0], wt[1]
			if tx >= 0 && tx < g.mapWidth && ty >= 0 && ty < g.mapHeight {
				if g.grid[ty][tx] == TileDestructibleWall {
					g.grid[ty][tx] = TileEmpty
				}
			}
		}
		g.mu.Unlock()
	}()
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
