package main

import (
	"math"
	"time"
)

// Ability is the interface implemented by all player active class skills (Ghosts or Pacmans).
type Ability interface {
	// UseAbility triggers the ability. Called from the tick loop with g.mu held.
	UseAbility(game *Game, caster *Player)
	// GetCooldownMs returns the cooldown duration in milliseconds.
	GetCooldownMs() int
	// IsReady reports whether the ability can be used right now.
	IsReady(now time.Time) bool
	// OnAssign is called when this role/ability is assigned to a player.
	OnAssign(caster *Player, now time.Time)
	// IsSpacebarTriggered returns true if the ability should be activated via spacebar.
	IsSpacebarTriggered() bool
	// SetUsed updates the ability's lastUsed timestamp.
	SetUsed(now time.Time)
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
	t.SetUsed(time.Now())
}

func (t *TrackerAbility) OnAssign(caster *Player, now time.Time) {
	t.lastUsed = now
	t.indicatorUntil = now
	caster.AbilityReady = now.Add(time.Duration(t.GetCooldownMs()) * time.Millisecond)
}

func (t *TrackerAbility) IsSpacebarTriggered() bool {
	return true
}

func (t *TrackerAbility) SetUsed(now time.Time) {
	t.lastUsed = now
	t.indicatorUntil = now.Add(TrackerIndicatorDurSec * time.Second)
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

func (b *BuilderAbility) OnAssign(caster *Player, now time.Time) {
	b.lastUsed = now
	caster.AbilityReady = now.Add(time.Duration(b.GetCooldownMs()) * time.Millisecond)
}

func (b *BuilderAbility) IsSpacebarTriggered() bool {
	return false
}

func (b *BuilderAbility) SetUsed(now time.Time) {
	b.lastUsed = now
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
	s.SetUsed(time.Now())

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

func (s *SprintAbility) OnAssign(caster *Player, now time.Time) {
	s.lastUsed = now
	caster.AbilityReady = now.Add(time.Duration(s.GetCooldownMs()) * time.Millisecond)
}

func (s *SprintAbility) IsSpacebarTriggered() bool {
	return true
}

func (s *SprintAbility) SetUsed(now time.Time) {
	s.lastUsed = now
}
