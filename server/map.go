package main

import (
	"math/rand"
	"time"
)

const minLeafSize = 12

// =============================================================================
// Rect — axis-aligned rectangle utility
// =============================================================================

type Rect struct{ X, Y, W, H int }

func (r Rect) CenterX() int { return r.X + r.W/2 }
func (r Rect) CenterY() int { return r.Y + r.H/2 }

// =============================================================================
// BSP node
// =============================================================================

type BSPNode struct {
	Rect        Rect
	Left, Right *BSPNode
	Room        *Rect // only set on leaf nodes
}

// =============================================================================
// Map size formula
// =============================================================================

// calcMapSize returns a square map side length proportional to player count.
//   n=4  → 60×60
//   n=8  → 84×84
//   n=12 → 108×108
func calcMapSize(n int) (width, height int) {
	size := 52 + (n-4)*7
	return size, size
}

// =============================================================================
// Public entry point
// =============================================================================

// GenerateMap produces a fully walled grid, carves BSP rooms and corridors,
// then scatters pellets on all reachable floor tiles.
func GenerateMap(width, height int) [][]int {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Start with all walls.
	grid := makeGrid(width, height, TileWall)

	// Build BSP tree over the inner area (leaving a 1-tile border of walls).
	root := &BSPNode{Rect: Rect{1, 1, width - 2, height - 2}}
	splitNode(root, rng)
	createRooms(root, grid, rng)
	connectRooms(root, grid)
	placePellets(grid, width, height, rng)

	return grid
}

// =============================================================================
// BSP partitioning
// =============================================================================

func splitNode(n *BSPNode, rng *rand.Rand) {
	r := n.Rect
	canSplitH := r.H > minLeafSize*2
	canSplitV := r.W > minLeafSize*2

	if !canSplitH && !canSplitV {
		return // Leaf — stop recursion
	}

	splitHorizontal := false
	if canSplitH && canSplitV {
		// Prefer splitting the longer axis; randomise when square-ish.
		if float64(r.H) > float64(r.W)*1.25 {
			splitHorizontal = true
		} else if float64(r.W) > float64(r.H)*1.25 {
			splitHorizontal = false
		} else {
			splitHorizontal = rng.Intn(2) == 0
		}
	} else {
		splitHorizontal = canSplitH
	}

	if splitHorizontal {
		// Split range: [minLeafSize, r.H - minLeafSize]
		rang := r.H - minLeafSize*2
		if rang <= 0 {
			return
		}
		split := minLeafSize + rng.Intn(rang+1)
		n.Left = &BSPNode{Rect: Rect{r.X, r.Y, r.W, split}}
		n.Right = &BSPNode{Rect: Rect{r.X, r.Y + split, r.W, r.H - split}}
	} else {
		rang := r.W - minLeafSize*2
		if rang <= 0 {
			return
		}
		split := minLeafSize + rng.Intn(rang+1)
		n.Left = &BSPNode{Rect: Rect{r.X, r.Y, split, r.H}}
		n.Right = &BSPNode{Rect: Rect{r.X + split, r.Y, r.W - split, r.H}}
	}

	splitNode(n.Left, rng)
	splitNode(n.Right, rng)
}

// =============================================================================
// Room creation in leaves
// =============================================================================

func createRooms(n *BSPNode, grid [][]int, rng *rand.Rand) {
	if n.Left == nil && n.Right == nil {
		// Leaf node: carve a room.
		r := n.Rect
		maxW := r.W - 2
		maxH := r.H - 2
		if maxW < 4 || maxH < 4 {
			return
		}
		minW := maxI(4, maxW*6/10)
		minH := maxI(4, maxH*6/10)

		roomW := randRange(rng, minW, maxW)
		roomH := randRange(rng, minH, maxH)

		xRange := r.W - roomW - 2
		yRange := r.H - roomH - 2

		roomX := r.X + 1
		if xRange > 0 {
			roomX += rng.Intn(xRange)
		}
		roomY := r.Y + 1
		if yRange > 0 {
			roomY += rng.Intn(yRange)
		}

		room := &Rect{roomX, roomY, roomW, roomH}
		n.Room = room

		// Carve floor.
		for y := roomY; y < roomY+roomH; y++ {
			for x := roomX; x < roomX+roomW; x++ {
				grid[y][x] = TileEmpty
			}
		}
		return
	}
	if n.Left != nil {
		createRooms(n.Left, grid, rng)
	}
	if n.Right != nil {
		createRooms(n.Right, grid, rng)
	}
}

// =============================================================================
// Corridor connection
// =============================================================================

// connectRooms links sibling rooms with L-shaped corridors (2-tiles wide).
func connectRooms(n *BSPNode, grid [][]int) {
	if n.Left == nil || n.Right == nil {
		return
	}
	connectRooms(n.Left, grid)
	connectRooms(n.Right, grid)

	r1 := findRoom(n.Left)
	r2 := findRoom(n.Right)
	if r1 == nil || r2 == nil {
		return
	}

	cx1, cy1 := r1.CenterX(), r1.CenterY()
	cx2, cy2 := r2.CenterX(), r2.CenterY()

	// Horizontal segment at cy1, then vertical segment at cx2.
	carveH(grid, cx1, cx2, cy1)
	carveV(grid, cy1, cy2, cx2)
}

// findRoom returns any leaf room reachable from n.
func findRoom(n *BSPNode) *Rect {
	if n == nil {
		return nil
	}
	if n.Room != nil {
		return n.Room
	}
	if r := findRoom(n.Left); r != nil {
		return r
	}
	return findRoom(n.Right)
}

// setTile sets grid[y][x] = val when in bounds (safe helper).
func setTile(grid [][]int, x, y, val int) {
	if y >= 0 && y < len(grid) && x >= 0 && x < len(grid[y]) {
		grid[y][x] = val
	}
}

// carveH carves a 2-tile-high horizontal passage from x1 to x2 at rows y and y+1.
func carveH(grid [][]int, x1, x2, y int) {
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	for x := x1; x <= x2; x++ {
		setTile(grid, x, y, TileEmpty)
		setTile(grid, x, y+1, TileEmpty) // second row
	}
}

// carveV carves a 2-tile-wide vertical passage from y1 to y2 at columns x and x+1.
func carveV(grid [][]int, y1, y2, x int) {
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	for y := y1; y <= y2; y++ {
		setTile(grid, x, y, TileEmpty)
		setTile(grid, x+1, y, TileEmpty) // second column
	}
}

// =============================================================================
// Pellet placement
// =============================================================================

// placePellets scatters pellets on ~35% of floor tiles.
// Corridor tiles receive a pellet with lower probability (15%) to avoid
// cluttering narrow passages.
func placePellets(grid [][]int, width, height int, rng *rand.Rand) {
	for y := 1; y < height-1; y++ {
		for x := 1; x < width-1; x++ {
			if grid[y][x] != TileEmpty {
				continue
			}
			// Check how many neighbours are walls (low count ⟹ room interior).
			wallNeighbours := 0
			for _, d := range [][2]int{{0, 1}, {0, -1}, {1, 0}, {-1, 0}} {
				nx, ny := x+d[0], y+d[1]
				if nx < 0 || nx >= width || ny < 0 || ny >= height {
					continue
				}
				if grid[ny][nx] == TileWall {
					wallNeighbours++
				}
			}
			// Corridor tile (≥2 wall neighbours on cardinal dirs) → 15%.
			threshold := 35
			if wallNeighbours >= 2 {
				threshold = 15
			}
			if rng.Intn(100) < threshold {
				grid[y][x] = TilePellet
			}
		}
	}
}

// =============================================================================
// Utilities
// =============================================================================

func makeGrid(width, height, val int) [][]int {
	grid := make([][]int, height)
	for y := range grid {
		grid[y] = make([]int, width)
		for x := range grid[y] {
			grid[y][x] = val
		}
	}
	return grid
}

func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// randRange returns a random int in [lo, hi] (inclusive).
func randRange(rng *rand.Rand, lo, hi int) int {
	if lo >= hi {
		return lo
	}
	return lo + rng.Intn(hi-lo+1)
}
