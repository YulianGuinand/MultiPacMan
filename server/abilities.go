package main

import (
	"math"
	"time"
)

// GhostAbility is the interface implemented by all ghost class skills.
type GhostAbility interface {
	// UseAbility triggers the ability. Called from the tick loop with g.mu held.
	UseAbility(game *Game, caster *Player)
	// GetCooldownMs returns the cooldown duration in milliseconds.
	GetCooldownMs() int
	// IsReady reports whether the ability can be used right now.
	IsReady(now time.Time) bool
}

// =============================================================================
// Tracker ability — Directional indicator
// =============================================================================
// The Tracker activates a 30-second directional indicator that points toward
// the nearest Pacman (including invisible ones). The indicator angle is
// computed each tick in buildStateForLocked and sent via PlayerStatus.
// Cooldown: 60 seconds.

type TrackerAbility struct {
	lastUsed       time.Time
	indicatorUntil time.Time // end of the active indicator window
}

func NewTrackerAbility() *TrackerAbility { return &TrackerAbility{} }

func (t *TrackerAbility) GetCooldownMs() int { return TrackerIndicatorCDMs }

func (t *TrackerAbility) IsReady(now time.Time) bool {
	return now.After(t.lastUsed.Add(time.Duration(t.GetCooldownMs()) * time.Millisecond))
}

func (t *TrackerAbility) UseAbility(g *Game, caster *Player) {
	t.lastUsed = time.Now()
	t.indicatorUntil = t.lastUsed.Add(TrackerIndicatorDurSec * time.Second)
	caster.AbilityReady = t.lastUsed.Add(time.Duration(t.GetCooldownMs()) * time.Millisecond)
}

// IsIndicatorActive returns true during the 30s active window after ability use.
func (t *TrackerAbility) IsIndicatorActive(now time.Time) bool {
	return now.Before(t.indicatorUntil)
}

// =============================================================================
// Builder ability — Destructible wall placement
// =============================================================================
// Places a TileDestructibleWall 2 tiles ahead of the Builder.
// The wall automatically collapses after 15 seconds.
// Cooldown: 20 seconds.

type BuilderAbility struct {
	lastUsed time.Time
}

func NewBuilderAbility() *BuilderAbility { return &BuilderAbility{} }

func (b *BuilderAbility) GetCooldownMs() int { return 20_000 }

func (b *BuilderAbility) IsReady(now time.Time) bool {
	return now.After(b.lastUsed.Add(time.Duration(b.GetCooldownMs()) * time.Millisecond))
}

func (b *BuilderAbility) UseAbility(g *Game, caster *Player) {
	b.lastUsed = time.Now()

	cx := int(math.Round(caster.X))
	cy := int(math.Round(caster.Y))

	// Snap the facing direction to the dominant cardinal axis.
	dirX, dirY := caster.DirX, caster.DirY
	if dirX == 0 && dirY == 0 {
		dirY = 1 // Default: face down.
	}
	var faceX, faceY int
	if math.Abs(dirX) >= math.Abs(dirY) {
		if dirX > 0 {
			faceX = 1
		} else {
			faceX = -1
		}
	} else {
		if dirY > 0 {
			faceY = 1
		} else {
			faceY = -1
		}
	}

	// The two wall tiles: builder's own tile + one in their facing direction.
	walls := [2][2]int{
		{cx, cy},
		{cx + faceX, cy + faceY},
	}

	// Place both walls (overwriting pellets if necessary).
	for _, wt := range walls {
		tx, ty := wt[0], wt[1]
		if tx < 1 || tx >= g.mapWidth-1 || ty < 1 || ty >= g.mapHeight-1 {
			continue
		}
		if g.grid[ty][tx] == TilePellet {
			g.remainingPellets--
		}
		g.grid[ty][tx] = TileDestructibleWall
	}

	// Push the builder to the nearest free adjacent tile.
	// Order of preference: backward → left perpendicular → right perpendicular → all cardinals.
	pushCandidates := [][2]int{
		{-faceX, -faceY},     // Backward (most natural: opposite of wall direction)
		{-faceY, faceX},      // Left perpendicular
		{faceY, -faceX},      // Right perpendicular
		{-1, 0}, {1, 0}, {0, -1}, {0, 1}, // Remaining cardinals
		{-1, -1}, {1, -1}, {-1, 1}, {1, 1}, // Diagonals (last resort)
	}

	pushed := false
	for _, off := range pushCandidates {
		newTX := cx + off[0]
		newTY := cy + off[1]
		if newTX < 0 || newTX >= g.mapWidth || newTY < 0 || newTY >= g.mapHeight {
			continue
		}
		t := g.grid[newTY][newTX]
		if t == TileWall || t == TileDestructibleWall {
			continue
		}
		// Free tile — move builder here (centred inside the tile).
		caster.X = float64(newTX) + 0.5
		caster.Y = float64(newTY) + 0.5
		pushed = true
		break
	}

	if !pushed {
		// Completely surrounded: apply a brief stun (rare edge case).
		caster.IsStunned = true
		caster.StunUntil = time.Now().Add(500 * time.Millisecond)
	}

	// Schedule both walls' auto-destruction after 15 seconds.
	done := g.done
	wallsCopy := walls
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


// =============================================================================
// Sprint ability — Dash
// =============================================================================
// Launches the Sprinter in their current direction at 3× speed for 0.3 seconds.
// If they collide with a wall mid-dash they self-stun for StunSeconds.
// Cooldown: 8 seconds.

type SprintAbility struct {
	lastUsed time.Time
}

func NewSprintAbility() *SprintAbility { return &SprintAbility{} }

func (s *SprintAbility) GetCooldownMs() int { return 8_000 }

func (s *SprintAbility) IsReady(now time.Time) bool {
	return now.After(s.lastUsed.Add(time.Duration(s.GetCooldownMs()) * time.Millisecond))
}

func (s *SprintAbility) UseAbility(g *Game, caster *Player) {
	s.lastUsed = time.Now()
	caster.AbilityReady = s.lastUsed.Add(time.Duration(s.GetCooldownMs()) * time.Millisecond)

	dirX, dirY := caster.DirX, caster.DirY
	if dirX == 0 && dirY == 0 {
		return // No direction held — dash does nothing.
	}

	// Normalise direction vector.
	length := math.Sqrt(dirX*dirX + dirY*dirY)
	caster.IsDashing = true
	caster.DashUntil = time.Now().Add(300 * time.Millisecond)
	caster.DashDirX = dirX / length
	caster.DashDirY = dirY / length
}
