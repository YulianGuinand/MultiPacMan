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

func (b *BuilderAbility) GetCooldownMs() int { return 30_000 }

func (b *BuilderAbility) IsReady(now time.Time) bool {
	return now.After(b.lastUsed.Add(time.Duration(b.GetCooldownMs()) * time.Millisecond))
}

func (b *BuilderAbility) UseAbility(g *Game, caster *Player) {
	// Builder uses click-to-build via handleBuild instead of dash/space.
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
	caster.DashRemainingTicks = 9
	caster.DashDirX = dirX / length
	caster.DashDirY = dirY / length
}
