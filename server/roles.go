package main

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"log"
	"math"
	"math/rand"
	"time"
)

// RoleDefinition configures attributes and setup details for a specific player role.
type RoleDefinition struct {
	Role         string
	Group        string // "PACMAN" or "GHOST"
	VisionRadius float64
	Speed        float64
	NewAbility   func() Ability
}

// RoleRegistry defines configurations for all available roles in the game.
var RoleRegistry = map[string]RoleDefinition{
	RolePacman: {
		Role:         RolePacman,
		Group:        "PACMAN",
		VisionRadius: VisionPacman,
		Speed:        SpeedPacman,
		NewAbility:   func() Ability { return nil },
	},
	RoleTracker: {
		Role:         RoleTracker,
		Group:        "GHOST",
		VisionRadius: VisionTracker,
		Speed:        SpeedTracker,
		NewAbility:   func() Ability { return NewTrackerAbility() },
	},
	RoleBuilder: {
		Role:         RoleBuilder,
		Group:        "GHOST",
		VisionRadius: VisionBuilder,
		Speed:        SpeedBuilder,
		NewAbility:   func() Ability { return NewBuilderAbility() },
	},
	RoleSprinter: {
		Role:         RoleSprinter,
		Group:        "GHOST",
		VisionRadius: VisionSprinter,
		Speed:        SpeedSprinter,
		NewAbility:   func() Ability { return NewSprintAbility() },
	},
}

// AssignRoles distributes roles to all players according to the formula:
//   Pacmans P = max(1, N/4)
//   Ghosts  F = N - P  (distributed round-robin from available GHOST roles)
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

	// Retrieve and shuffle ghost roles dynamically from the registry.
	var ghostRoles []string
	for r, def := range RoleRegistry {
		if def.Group == "GHOST" {
			ghostRoles = append(ghostRoles, r)
		}
	}
	rng.Shuffle(len(ghostRoles), func(i, j int) { ghostRoles[i], ghostRoles[j] = ghostRoles[j], ghostRoles[i] })
	ghostIdx := 0

	// Find well-distributed spawn positions.
	spawnPoints := findSpawnPoints(grid, mapWidth, mapHeight, n, rng)

	for i, id := range ids {
		p := players[id]

		var def RoleDefinition
		if i < pacmanCount {
			def = RoleRegistry[RolePacman]
		} else {
			role := ghostRoles[ghostIdx%len(ghostRoles)]
			ghostIdx++
			def = RoleRegistry[role]
		}

		p.Role = def.Role
		p.VisionRadius = def.VisionRadius
		p.Speed = def.Speed
		p.Ability = def.NewAbility()
		if p.Ability != nil {
			p.Ability.OnAssign(p, time.Now())
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
