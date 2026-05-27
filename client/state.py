"""
state.py — Thread-safe shared game state.

The GameState object is the single source of truth shared between:
  - The network thread (writes via update_* methods)
  - The Pyxel render thread (reads via get_snapshot)

All mutations go through the internal lock. get_snapshot() returns a
plain-dict copy so the render thread never holds the lock while drawing.
"""
from __future__ import annotations

import threading
from dataclasses import dataclass, field
from typing import Optional


# ---------------------------------------------------------------------------
# Sub-structures (used inside snapshots — plain dataclasses, no locking)
# ---------------------------------------------------------------------------

@dataclass
class PlayerData:
    id: str
    x: float
    y: float
    revealed_role: Optional[str] = None


@dataclass
class FootprintData:
    """3-second-old Pacman position (Tracker ability)."""
    id: str
    x: float
    y: float


@dataclass
class CherryData:
    """Cherry item visible to Pacman."""
    id: str
    x: float
    y: float


@dataclass
class ChestData:
    """Chest item visible to Pacman."""
    id: str
    x: float
    y: float


@dataclass
class GameStatus:
    score: int = 0
    stunned: bool = False
    cooldown_ms: int = 0
    role: str = ""
    lives: int = 0
    invis_charges: int = 0
    is_invisible: bool = False
    is_dead: bool = False
    cherry_dir_angle: float = -999.0
    tracker_dir_angle: float = -999.0
    spectating_id: str = ""


@dataclass
class LobbyPlayerData:
    id: str
    ready: bool


# ---------------------------------------------------------------------------
# GameState
# ---------------------------------------------------------------------------

class GameState:
    """Central shared state between the network thread and Pyxel."""

    # Tile type constants (mirror server)
    TILE_EMPTY = 0
    TILE_WALL = 1
    TILE_PELLET = 2
    TILE_DESTRUCTIBLE = 3
    TILE_CHERRY = 4
    TILE_CHEST = 5

    def __init__(self) -> None:
        self._lock = threading.Lock()

        # --- Connection ---
        self.connected: bool = False
        self.local_id: Optional[str] = None
        self.room_id: Optional[str] = None

        # --- Phase ---
        self.room_state: str = "LOBBY"

        # --- Lobby ---
        self.lobby_players: list[LobbyPlayerData] = []
        self.min_players: int = 4
        self.max_players: int = 12

        # --- Playing ---
        self.players: dict[str, PlayerData] = {}
        self.footprints: list[FootprintData] = []
        self.cherries: list[CherryData] = []
        self.chests: list[ChestData] = []
        # tile_cache: (x, y) → tile_type — additive, tiles stay until overridden
        self.tile_cache: dict[tuple[int, int], int] = {}
        self.status: GameStatus = GameStatus()
        self.map_width: int = 0
        self.map_height: int = 0
        self.last_tick: int = 0
        self.last_seq: int = 0

        # Client-side predicted position (updated in Pyxel thread)
        self.predicted_x: float = 0.0
        self.predicted_y: float = 0.0

        # Client-side camera tracking (updated in Pyxel thread)
        self.camera_x: float = 0.0
        self.camera_y: float = 0.0

        # Client-side builder selection state (updated in Pyxel thread)
        self.builder_step: int = 0
        self.builder_x1: int = -1
        self.builder_y1: int = -1

        # --- Finished ---
        self.winner: Optional[str] = None
        self.scores: dict[str, int] = {}
        self.reveals: dict[str, str] = {}

        # --- Misc ---
        self.last_error: Optional[str] = None

    # -----------------------------------------------------------------------
    # Write methods (called from network thread)
    # -----------------------------------------------------------------------

    def update_welcome(self, data: dict) -> None:
        with self._lock:
            self.local_id = data["your_id"]
            self.room_id = data["room_id"]
            self.room_state = data["room_state"]
            self.connected = True

    def update_lobby(self, data: dict) -> None:
        with self._lock:
            self.lobby_players = [
                LobbyPlayerData(id=p["id"], ready=p["ready"])
                for p in data.get("players", [])
            ]
            self.min_players = data.get("min_ready", 4)
            self.max_players = data.get("max_players", 12)
            self.room_state = "LOBBY"

    def update_game_start(self, data: dict) -> None:
        with self._lock:
            self.room_state = "PLAYING"
            self.status.role = data["your_role"]
            self.map_width = data["map_width"]
            self.map_height = data["map_height"]
            sx, sy = data["spawn_x"], data["spawn_y"]
            self.predicted_x = sx
            self.predicted_y = sy
            # Seed our own entry in the players dict.
            if self.local_id:
                self.players[self.local_id] = PlayerData(
                    id=self.local_id, x=sx, y=sy
                )

    def update_game_state(self, data: dict) -> None:
        with self._lock:
            self.last_tick = data.get("tick", 0)
            self.last_seq = data.get("last_seq", 0)

            # Players
            new_players: dict[str, PlayerData] = {}
            for p in data.get("players", []):
                new_players[p["id"]] = PlayerData(
                    id=p["id"],
                    x=p["x"],
                    y=p["y"],
                    revealed_role=p.get("revealed_role"),
                )
            self.players = new_players

            # Tiles (additive cache cleared each tick to implement strict fog of war)
            self.tile_cache.clear()
            for t in data.get("tiles", []):
                self.tile_cache[(t["x"], t["y"])] = t["t"]

            # Footprints (legacy — kept for compatibility)
            self.footprints = [
                FootprintData(id=f["id"], x=f["x"], y=f["y"])
                for f in data.get("footprints", [])
            ]

            # Cherries (Pacman only)
            self.cherries = [
                CherryData(id=c["id"], x=c["x"], y=c["y"])
                for c in data.get("cherries", [])
            ]

            # Chests (Pacman only)
            self.chests = [
                ChestData(id=c["id"], x=c["x"], y=c["y"])
                for c in data.get("chests", [])
            ]

            # Status
            s = data.get("status", {})
            self.status.score = s.get("score", 0)
            self.status.stunned = s.get("stunned", False)
            self.status.cooldown_ms = s.get("cooldown_ms", 0)
            if s.get("role"):
                self.status.role = s["role"]
            self.status.lives = s.get("lives", 0)
            self.status.invis_charges = s.get("invis_charges", 0)
            self.status.is_invisible = s.get("is_invisible", False)
            self.status.is_dead = s.get("is_dead", False)
            self.status.cherry_dir_angle = s.get("cherry_dir_angle", -999.0)
            self.status.tracker_dir_angle = s.get("tracker_dir_angle", -999.0)
            self.status.spectating_id = s.get("spectating_id", "")

            # Server reconciliation for local player.
            # With client-side wall collision, discrepancy should be < 0.1 tiles
            # in normal play. Large gaps indicate wall penetration or teleport.
            if self.local_id and self.local_id in self.players:
                srv = self.players[self.local_id]
                dx = srv.x - self.predicted_x
                dy = srv.y - self.predicted_y
                dist = (dx * dx + dy * dy) ** 0.5
                if dist > 2.5:
                    # Very large gap: hard snap (e.g. respawn, stun teleport).
                    self.predicted_x = srv.x
                    self.predicted_y = srv.y
                elif dist > 0.15:
                    # Small accumulated error: gentle 10% pull toward server.
                    self.predicted_x += dx * 0.10
                    self.predicted_y += dy * 0.10

    def update_game_over(self, data: dict) -> None:
        with self._lock:
            self.room_state = "FINISHED"
            self.winner = data.get("winner")
            self.scores = data.get("scores", {})
            self.reveals = data.get("reveals", {})

    def set_error(self, message: str) -> None:
        with self._lock:
            self.last_error = message

    def clear_error(self) -> None:
        with self._lock:
            self.last_error = None

    # -----------------------------------------------------------------------
    # Read methods (called from Pyxel thread)
    # -----------------------------------------------------------------------

    def apply_local_prediction(self, dir_x: float, dir_y: float, speed: float) -> None:
        """Apply client-side prediction with wall collision against the known tile cache."""
        with self._lock:
            # Don't predict if dead.
            if self.status.is_dead:
                return

            new_x = self.predicted_x + dir_x * speed
            new_y = self.predicted_y + dir_y * speed

            # Client-side wall collision: try combined move first, then axis-by-axis.
            if not self._would_collide(new_x, new_y):
                self.predicted_x = new_x
                self.predicted_y = new_y
            else:
                # Try sliding along each axis independently.
                if not self._would_collide(new_x, self.predicted_y):
                    self.predicted_x = new_x
                elif not self._would_collide(self.predicted_x, new_y):
                    self.predicted_y = new_y
                # else: fully blocked, don't move.

            # Clamp to map bounds.
            if self.map_width > 0:
                self.predicted_x = max(0.4, min(self.map_width - 1.4, self.predicted_x))
                self.predicted_y = max(0.4, min(self.map_height - 1.4, self.predicted_y))

    def _would_collide(self, x: float, y: float) -> bool:
        """Return True if the player circle at (x, y) overlaps a known solid tile.

        Unknown tiles (not yet in cache) are treated as passable — the server
        is authoritative and will correct the position if needed.
        """
        RADIUS = 0.35
        corners = (
            (x - RADIUS, y - RADIUS),
            (x + RADIUS, y - RADIUS),
            (x - RADIUS, y + RADIUS),
            (x + RADIUS, y + RADIUS),
        )
        for cx, cy in corners:
            tx, ty = int(cx), int(cy)
            tile = self.tile_cache.get((tx, ty))
            if tile is not None and tile in (self.TILE_WALL, self.TILE_DESTRUCTIBLE):
                return True
        return False

    def get_snapshot(self) -> dict:
        """Return a consistent deep-enough copy of the state for one frame."""
        with self._lock:
            return {
                "connected": self.connected,
                "local_id": self.local_id,
                "room_id": self.room_id,
                "room_state": self.room_state,
                "lobby_players": list(self.lobby_players),
                "min_players": self.min_players,
                "max_players": self.max_players,
                "players": dict(self.players),
                "footprints": list(self.footprints),
                "cherries": list(self.cherries),
                "chests": list(self.chests),
                "tile_cache": dict(self.tile_cache),
                "status": GameStatus(
                    score=self.status.score,
                    stunned=self.status.stunned,
                    cooldown_ms=self.status.cooldown_ms,
                    role=self.status.role,
                    lives=self.status.lives,
                    invis_charges=self.status.invis_charges,
                    is_invisible=self.status.is_invisible,
                    is_dead=self.status.is_dead,
                    cherry_dir_angle=self.status.cherry_dir_angle,
                    tracker_dir_angle=self.status.tracker_dir_angle,
                    spectating_id=self.status.spectating_id,
                ),
                "map_width": self.map_width,
                "map_height": self.map_height,
                "predicted_x": self.predicted_x,
                "predicted_y": self.predicted_y,
                "camera_x": self.camera_x,
                "camera_y": self.camera_y,
                "builder_step": self.builder_step,
                "builder_x1": self.builder_x1,
                "builder_y1": self.builder_y1,
                "winner": self.winner,
                "scores": dict(self.scores),
                "reveals": dict(self.reveals),
                "last_error": self.last_error,
            }
