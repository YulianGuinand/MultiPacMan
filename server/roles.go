package main

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"log"
	"math"
	"math/rand"
	"time"
)

// =============================================================================
// Role assignment
// =============================================================================

// AssignRoles distributes roles to all players according to the formula:
//   Pacmans P = max(1, N/4)
//   Ghosts  F = N - P  (distributed round-robin: Tracker / Builder / Sprinter)
// It also sets per-player attributes (speed, vision radius, ability) and
// assigns spread-out spawn positions to minimise immediate conflicts.
func AssignRoles(players map[string]*Player, grid [][]int, mapWidth, mapHeight int) {
	n := len(players)
	pacmanCount := maxI(1, n/4)

	// Seed PRNG using crypto/rand to guarantee true randomness
	var seed int64
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		log.Printf("[RoleAssign] Warning: failed to read crypto/rand, falling back to time-based seed: %v", err)
		seed = time.Now().UnixNano()
	} else {
		seed = int64(binary.BigEndian.Uint64(b[:]))
	}
	rng := rand.New(rand.NewSource(seed))

	// Collect and shuffle IDs to randomise who gets which role.
	ids := make([]string, 0, n)
	for id := range players {
		ids = append(ids, id)
	}
	rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })

	// Ghost subclass rotation.
	ghostRoles := []string{RoleTracker, RoleBuilder, RoleSprinter}
	rng.Shuffle(len(ghostRoles), func(i, j int) { ghostRoles[i], ghostRoles[j] = ghostRoles[j], ghostRoles[i] })
	ghostIdx := 0

	// Find well-distributed spawn positions.
	spawnPoints := findSpawnPoints(grid, mapWidth, mapHeight, n, rng)

	for i, id := range ids {
		p := players[id]

		if i < pacmanCount {
			p.Role = RolePacman
			p.VisionRadius = VisionPacman
			p.Speed = SpeedPacman
			p.Ability = nil
		} else {
			role := ghostRoles[ghostIdx%len(ghostRoles)]
			ghostIdx++
			p.Role = role

			switch role {
			case RoleTracker:
				p.VisionRadius = VisionTracker
				p.Speed = SpeedTracker
				ta := NewTrackerAbility()
				ta.lastUsed = time.Now()
				p.Ability = ta
				p.AbilityReady = ta.lastUsed.Add(time.Duration(ta.GetCooldownMs()) * time.Millisecond)
			case RoleBuilder:
				p.VisionRadius = VisionBuilder
				p.Speed = SpeedBuilder
				ba := NewBuilderAbility()
				ba.lastUsed = time.Now()
				p.Ability = ba
				p.AbilityReady = ba.lastUsed.Add(time.Duration(ba.GetCooldownMs()) * time.Millisecond)
			case RoleSprinter:
				p.VisionRadius = VisionSprinter
				p.Speed = SpeedSprinter
				sa := NewSprintAbility()
				sa.lastUsed = time.Now()
				p.Ability = sa
				p.AbilityReady = sa.lastUsed.Add(time.Duration(sa.GetCooldownMs()) * time.Millisecond)
			}
		}

		// Set spawn position.
		if i < len(spawnPoints) {
			p.X = float64(spawnPoints[i][0]) + 0.5
			p.Y = float64(spawnPoints[i][1]) + 0.5
		} else {
			// Fallback: centre of the map with slight offset.
			p.X = float64(mapWidth/2) + float64(i)*2
			p.Y = float64(mapHeight / 2)
		}

		log.Printf("[RoleAssign] Assigned role %s to player %s at spawn point (%.1f, %.1f)", p.Role, id, p.X, p.Y)
	}
}

// =============================================================================
// Spawn position selection
// =============================================================================

// findSpawnPoints returns n floor tile positions that are as spread out as
// possible, using a greedy furthest-point selection.
func findSpawnPoints(grid [][]int, mapWidth, mapHeight, n int, rng *rand.Rand) [][2]int {
	// Collect all walkable tiles.
	var walkable [][2]int
	for y := 1; y < mapHeight-1; y++ {
		for x := 1; x < mapWidth-1; x++ {
			if grid[y][x] != TileWall {
				walkable = append(walkable, [2]int{x, y})
			}
		}
	}
	if len(walkable) == 0 || n == 0 {
		return nil
	}

	// Seed: pick a random starting tile.
	result := make([][2]int, 0, n)
	first := walkable[rng.Intn(len(walkable))]
	result = append(result, first)

	// Greedy furthest-point selection.
	for len(result) < n && len(result) < len(walkable) {
		bestDist := -1.0
		bestIdx := 0
		for i, pt := range walkable {
			// Min distance from pt to any already-chosen spawn.
			minD := math.MaxFloat64
			for _, chosen := range result {
				d := tileDist(pt, chosen)
				if d < minD {
					minD = d
				}
			}
			if minD > bestDist {
				bestDist = minD
				bestIdx = i
			}
		}
		result = append(result, walkable[bestIdx])
	}
	return result
}

func tileDist(a, b [2]int) float64 {
	dx := float64(a[0] - b[0])
	dy := float64(a[1] - b[1])
	return math.Sqrt(dx*dx + dy*dy)
}
