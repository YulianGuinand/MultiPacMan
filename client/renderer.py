"""
renderer.py — Pyxel rendering using geometric primitives only.

Coordinate system:
    World space : float tile coordinates (0 … mapWidth-1, 0 … mapHeight-1)
    Screen space : pixels, top-left = (0, 0)
    Conversion   : screen_x = world_x * TILE_SIZE - camera_x

Pyxel 16-colour palette reference:
    0  black       4  brown       8  red        12 blue
    1  dark blue   5  dark gray   9  orange     13 indigo
    2  dark purple 6  light gray  10 yellow     14 pink
    3  dark green  7  white       11 green      15 peach
"""
from __future__ import annotations

import math

import pyxel

from state import GameState, PlayerData, FootprintData, GameStatus

# ---------------------------------------------------------------------------
# Layout constants
# ---------------------------------------------------------------------------

SCREEN_W = 256
SCREEN_H = 240
TILE_SIZE = 8          # pixels per world tile
PLAYER_R = 3           # player circle radius in pixels
HUD_H = 32             # bottom HUD height in pixels (increased for new info)
GAME_H = SCREEN_H - HUD_H  # usable game viewport height

# ---------------------------------------------------------------------------
# Colour palette
# ---------------------------------------------------------------------------

C_BG         = 0   # Black — background / void
C_FLOOR      = 1   # Dark blue — walkable floor
C_WALL       = 5   # Dark gray — solid wall
C_WALL_TRIM  = 6   # Light gray — wall highlight edge
C_DWALL      = 4   # Brown — destructible wall
C_PELLET     = 10  # Yellow — pellet dot
C_SELF       = 11  # Green — local player (ghost)
C_SELF_PAC   = 9   # Orange — local player (Pacman)
C_OTHER      = 6   # Light gray — other players (unknown role)
C_PAC_REV    = 9   # Orange — revealed Pacman
C_GHOST_REV  = 12  # Blue — revealed ghost
C_STUN       = 8   # Red — stun flash
C_FOOTPRINT  = 13  # Indigo — Tracker thermal footprint
C_HUD_BG     = 1   # Dark blue — HUD background
C_HUD_TEXT   = 7   # White — HUD text
C_HUD_DIM    = 5   # Dark gray — secondary HUD text
C_TITLE      = 9   # Orange — title text
C_READY      = 11  # Green — ready indicator
C_WAITING    = 5   # Dark gray — waiting indicator
C_CHERRY     = 8   # Red — cherry body
C_CHERRY_STEM = 3  # Dark green — cherry stem
C_CHEST      = 10  # Yellow/gold — chest
C_CHEST_TRIM = 4   # Brown — chest outline
C_INDICATOR  = 14  # Pink — directional indicator arrow
C_TRACKER_IND = 8  # Red — tracker directional indicator
C_INVIS_TEXT = 13  # Indigo — invisibility HUD text
C_DEAD_TEXT  = 8   # Red — death text
C_LIVES      = 8   # Red — lives heart

# Vision radii in tiles matching server
VISION_RADII = {
    "PACMAN": 15.0,
    "GHOST_TRACKER": 9.0,
    "GHOST_BUILDER": 9.0,
    "GHOST_SPRINTER": 12.0,
    "GHOST_TRAPPER": 10.0,
    "GHOST_PHASER": 10.0,
}


class Renderer:
    """Stateful renderer; call draw() once per Pyxel frame."""

    def __init__(self, state: GameState) -> None:
        self.state = state
        self._cam_x: float = 0.0  # float for smooth lerp
        self._cam_y: float = 0.0

    # -----------------------------------------------------------------------
    # Main draw dispatch
    # -----------------------------------------------------------------------

    def draw(self) -> None:
        snap = self.state.get_snapshot()
        pyxel.cls(C_BG)

        if not snap["connected"]:
            self._draw_connecting(snap)
            pyxel.mouse(False)
        elif snap["room_state"] == "LOBBY":
            self._draw_lobby(snap)
            pyxel.mouse(False)
        elif snap["room_state"] == "PLAYING":
            self._draw_game(snap)
        elif snap["room_state"] == "FINISHED":
            self._draw_game_over(snap)
            pyxel.mouse(False)

        # Error overlay (always on top).
        if snap["last_error"]:
            self._draw_error(snap["last_error"])

    # -----------------------------------------------------------------------
    # Connecting screen
    # -----------------------------------------------------------------------

    def _draw_connecting(self, snap: dict) -> None:
        self._draw_title(SCREEN_W // 2, 70)
        msg = "Connecting to server"
        dots = "." * ((pyxel.frame_count // 8) % 4)
        self._ctext(msg + dots, SCREEN_W // 2, 100, C_HUD_DIM)
        self._ctext("wss://pacman.yulian-server.duckdns.org", SCREEN_W // 2, 112, C_WAITING)

    # -----------------------------------------------------------------------
    # Lobby screen
    # -----------------------------------------------------------------------

    def _draw_lobby(self, snap: dict) -> None:
        self._draw_title(SCREEN_W // 2, 18)

        room_id = snap.get("room_id") or "…"
        self._ctext(f"Room: {room_id}", SCREEN_W // 2, 32, C_HUD_DIM)

        # Player list
        y = 46
        pyxel.text(10, y, "Players", C_HUD_TEXT)
        pyxel.line(10, y + 6, SCREEN_W - 10, y + 6, C_WALL)
        y += 10

        players = snap["lobby_players"]
        for p in players:
            is_self = p.id == snap["local_id"]
            prefix = "\x10 " if is_self else "  "  # right arrow for self
            ready_str = "READY  " if p.ready else "waiting"
            col = C_READY if p.ready else C_WAITING
            id_col = C_HUD_TEXT if is_self else C_OTHER
            short_id = p.id[:10]
            pyxel.text(10, y, prefix + short_id, id_col)
            pyxel.text(SCREEN_W - 50, y, ready_str, col)
            y += 9

        # Status line
        total = len(players)
        min_p = snap["min_players"]
        max_p = snap["max_players"]
        bar_y = SCREEN_H - 55
        pyxel.line(10, bar_y, SCREEN_W - 10, bar_y, C_WALL)
        self._ctext(
            f"{total}/{max_p} players  (min {min_p} to start)",
            SCREEN_W // 2,
            bar_y + 5,
            C_HUD_DIM,
        )

        # Instructions
        self._ctext("ENTER / SPACE  →  toggle Ready", SCREEN_W // 2, SCREEN_H - 38, C_HUD_TEXT)
        self._ctext("S              →  Force start", SCREEN_W // 2, SCREEN_H - 28, C_HUD_TEXT)

        if total >= min_p:
            self._ctext("Enough players! Press S to start.", SCREEN_W // 2, SCREEN_H - 12, C_READY)
        else:
            need = min_p - total
            self._ctext(f"Need {need} more player(s)…", SCREEN_W // 2, SCREEN_H - 12, C_WAITING)

    # -----------------------------------------------------------------------
    # Game screen
    # -----------------------------------------------------------------------

    def _draw_game(self, snap: dict) -> None:
        status = snap["status"]
        pred_x = snap["predicted_x"]
        pred_y = snap["predicted_y"]
        local_id = snap["local_id"]
        role = status.role

        # Determine viewpoint position (interpolated)
        if status.is_dead and status.spectating_id:
            spectated = snap["players"].get(status.spectating_id)
            if spectated:
                prev = snap["prev_players"].get(status.spectating_id)
                if prev is not None:
                    display_x = prev.x + (spectated.x - prev.x) * snap["interp_t"]
                    display_y = prev.y + (spectated.y - prev.y) * snap["interp_t"]
                else:
                    display_x, display_y = spectated.x, spectated.y
            else:
                display_x, display_y = pred_x, pred_y
        else:
            local_interp_t = snap.get("local_interp_t", 1.0)
            prev_x = snap.get("prev_predicted_x", pred_x)
            prev_y = snap.get("prev_predicted_y", pred_y)
            display_x = prev_x + (pred_x - prev_x) * local_interp_t
            display_y = prev_y + (pred_y - prev_y) * local_interp_t

        self._update_camera(display_x, display_y)
        self._draw_tiles(snap, display_x, display_y)
        self._draw_cherries(snap["cherries"])
        self._draw_chests(snap["chests"])
        self._draw_footprints(snap["footprints"])
        self._draw_other_players(snap, local_id)

        # Draw self sprite (unless dead)
        if not status.is_dead:
            self._draw_self(display_x, display_y, role, status.stunned, status.is_invisible)
            # Draw directional indicators around self
            sx, sy = self._world_to_screen(display_x, display_y)
            if role in ("PACMAN", "GHOST_BUILDER"):
                self._draw_direction_indicator(sx, sy, status.cherry_dir_angle, C_INDICATOR)
            else:
                self._draw_direction_indicator(sx, sy, status.tracker_dir_angle, C_TRACKER_IND)

        # Draw builder preview if playing, alive, and GHOST_BUILDER
        if role == "GHOST_BUILDER" and not status.is_dead:
            self._draw_builder_preview(snap)
        else:
            pyxel.mouse(False)

        self._draw_hud(status, snap)

        # Death overlay (on top of everything)
        if status.is_dead:
            self._draw_death_overlay(snap)

    def _update_camera(self, px: float, py: float) -> None:
        """Smoothly follow the local player with exponential lerp."""
        target_x = px * TILE_SIZE - SCREEN_W / 2
        target_y = py * TILE_SIZE - GAME_H / 2
        # 20% per frame → reaches target in ~5 frames, feels responsive yet smooth
        self._cam_x += (target_x - self._cam_x) * 0.20
        self._cam_y += (target_y - self._cam_y) * 0.20
        self.state.camera_x = self._cam_x
        self.state.camera_y = self._cam_y

    def _draw_builder_preview(self, snap: dict) -> None:
        status = snap["status"]
        if status.is_dead or status.stunned:
            pyxel.mouse(False)
            return

        pyxel.mouse(True)
        builder_step = snap.get("builder_step", 0)

        # Get current mouse hover tile
        grid_x = int((pyxel.mouse_x + self._cam_x) // TILE_SIZE)
        grid_y = int((pyxel.mouse_y + self._cam_y) // TILE_SIZE)

        # Bounds check
        if 0 <= grid_x < snap["map_width"] and 0 <= grid_y < snap["map_height"]:
            tile_type = snap["tile_cache"].get((grid_x, grid_y))
            # Screen coords for hover tile
            hsx = grid_x * TILE_SIZE - self._cam_x
            hsy = grid_y * TILE_SIZE - self._cam_y

            # Determine color based on availability and cooldown
            if status.cooldown_ms > 0:
                col = 5  # dark gray
            else:
                # If builder_step is 0, the first click must be Empty (0)
                # If builder_step is 1, the second click can be Empty (0) or Wall (1)
                is_valid = (tile_type in (0, 2)) if builder_step == 0 else (tile_type in (0, 1, 2))
                col = 11 if is_valid else 8

            # Pulsing hover box outline
            if (pyxel.frame_count // 5) % 2 == 0:
                pyxel.rectb(round(hsx), round(hsy), TILE_SIZE, TILE_SIZE, col)

        # If step == 1, draw the first clicked tile and the potential second tile
        if builder_step == 1:
            x1 = snap.get("builder_x1", -1)
            y1 = snap.get("builder_y1", -1)
            s1x = x1 * TILE_SIZE - self._cam_x
            s1y = y1 * TILE_SIZE - self._cam_y

            # Draw first block preview (blinking yellow/orange)
            col1 = 10 if (pyxel.frame_count // 4) % 2 == 0 else 9
            pyxel.rectb(round(s1x), round(s1y), TILE_SIZE, TILE_SIZE, col1)

            # Draw connector/line to mouse cursor
            mx = pyxel.mouse_x
            my = pyxel.mouse_y
            pyxel.line(round(s1x + 4), round(s1y + 4), mx, my, 13) # indigo connection line

            # Calculate proposed second block
            mg_x = int((pyxel.mouse_x + self._cam_x) // TILE_SIZE)
            mg_y = int((pyxel.mouse_y + self._cam_y) // TILE_SIZE)
            dx = mg_x - x1
            dy = mg_y - y1
            if dx != 0 or dy != 0:
                if abs(dx) >= abs(dy):
                    x2 = x1 + (1 if dx >= 0 else -1)
                    y2 = y1
                else:
                    x2 = x1
                    y2 = y1 + (1 if dy >= 0 else -1)

                s2x = x2 * TILE_SIZE - self._cam_x
                s2y = y2 * TILE_SIZE - self._cam_y
                tile_type2 = snap["tile_cache"].get((x2, y2))
                col2 = 11 if tile_type2 in (0, 1) else 8
                # Draw second block preview
                pyxel.rectb(round(s2x), round(s2y), TILE_SIZE, TILE_SIZE, col2)

    def _world_to_screen(self, wx: float, wy: float) -> tuple[int, int]:
        return (
            round(wx * TILE_SIZE - self._cam_x),
            round(wy * TILE_SIZE - self._cam_y),
        )

    def _draw_tiles(self, snap: dict, view_x: float, view_y: float) -> None:
        tile_cache = snap["tile_cache"]
        status = snap["status"]
        role = status.role
        
        if status.is_dead and status.spectating_id:
            spectated = snap["players"].get(status.spectating_id)
            if spectated:
                role = spectated.revealed_role or "GHOST_TRACKER"
                
        vision_radius = VISION_RADII.get(role, 9.0)
        vr_sq = vision_radius * vision_radius

        for (tx, ty), tile_type in tile_cache.items():
            sx = round(tx * TILE_SIZE - self._cam_x)
            sy = round(ty * TILE_SIZE - self._cam_y)
            # Frustum cull.
            if sx < -TILE_SIZE or sx >= SCREEN_W or sy < -TILE_SIZE or sy >= GAME_H:
                continue
                
            # Fog of War check: only draw currently visible tiles
            dx = (tx + 0.5) - view_x
            dy = (ty + 0.5) - view_y
            if (dx * dx + dy * dy) > vr_sq:
                continue
                
            self._draw_tile(sx, sy, tile_type)

    def _draw_tile(self, sx: int, sy: int, tile_type: int) -> None:
        ts = TILE_SIZE
        if tile_type == 1:  # Wall
            pyxel.rect(sx, sy, ts, ts, C_WALL)
            # Subtle top/left highlight bevel.
            pyxel.line(sx, sy, sx + ts - 1, sy, C_WALL_TRIM)
            pyxel.line(sx, sy, sx, sy + ts - 1, C_WALL_TRIM)
        elif tile_type == 3:  # Destructible wall
            pyxel.rect(sx, sy, ts, ts, C_DWALL)
            # Cross-hatch pattern to signal "breakable".
            pyxel.line(sx + 1, sy + 1, sx + ts - 2, sy + ts - 2, 8)
            pyxel.line(sx + ts - 2, sy + 1, sx + 1, sy + ts - 2, 8)
        elif tile_type == 0:  # Empty floor
            pyxel.rect(sx, sy, ts, ts, C_FLOOR)
        elif tile_type == 2:  # Pellet
            pyxel.rect(sx, sy, ts, ts, C_FLOOR)
            cx = sx + ts // 2
            cy = sy + ts // 2
            # 2×2 pellet dot.
            pyxel.pset(cx,     cy,     C_PELLET)
            pyxel.pset(cx + 1, cy,     C_PELLET)
            pyxel.pset(cx,     cy + 1, C_PELLET)
            pyxel.pset(cx + 1, cy + 1, C_PELLET)

    # -----------------------------------------------------------------------
    # Cherries & Chests
    # -----------------------------------------------------------------------

    def _draw_cherries(self, cherries: list) -> None:
        for c in cherries:
            sx, sy = self._world_to_screen(c.x, c.y)
            if not (-8 <= sx < SCREEN_W + 8 and -8 <= sy < GAME_H + 8):
                continue
            # Cherry body — two small circles
            pyxel.circ(sx - 1, sy + 1, 2, C_CHERRY)
            pyxel.circ(sx + 2, sy + 1, 2, C_CHERRY)
            # Highlights
            pyxel.pset(sx - 1, sy, 15)  # peach highlight
            pyxel.pset(sx + 2, sy, 15)
            # Stem
            pyxel.line(sx, sy - 1, sx + 1, sy - 3, C_CHERRY_STEM)
            pyxel.line(sx + 2, sy - 1, sx + 1, sy - 3, C_CHERRY_STEM)

    def _draw_chests(self, chests: list) -> None:
        for ch in chests:
            sx, sy = self._world_to_screen(ch.x, ch.y)
            if not (-8 <= sx < SCREEN_W + 8 and -8 <= sy < GAME_H + 8):
                continue
            # Chest body — small golden box
            pyxel.rect(sx - 3, sy - 2, 6, 5, C_CHEST)
            pyxel.rectb(sx - 3, sy - 2, 6, 5, C_CHEST_TRIM)
            # Latch
            pyxel.pset(sx, sy, C_CHEST_TRIM)
            # Subtle shimmer animation
            if (pyxel.frame_count // 10) % 3 == 0:
                pyxel.pset(sx - 2, sy - 1, 7)  # white sparkle

    # -----------------------------------------------------------------------
    # Directional indicator
    # -----------------------------------------------------------------------

    def _draw_direction_indicator(self, sx: int, sy: int, angle_rad: float, color: int) -> None:
        """Draw a small arrow orbiting around the player sprite, pointing outward."""
        if angle_rad <= -900:
            return  # no indicator

        dist = 14  # orbit distance from center in pixels
        ix = sx + math.cos(angle_rad) * dist
        iy = sy + math.sin(angle_rad) * dist

        # Triangle arrow pointing outward.
        # Tip = further out along the angle, base perpendicular.
        tip_dist = 4
        base_dist = 2
        perp = angle_rad + math.pi / 2

        tip_x = ix + math.cos(angle_rad) * tip_dist
        tip_y = iy + math.sin(angle_rad) * tip_dist
        base1_x = ix + math.cos(perp) * base_dist
        base1_y = iy + math.sin(perp) * base_dist
        base2_x = ix - math.cos(perp) * base_dist
        base2_y = iy - math.sin(perp) * base_dist

        pyxel.tri(
            round(tip_x), round(tip_y),
            round(base1_x), round(base1_y),
            round(base2_x), round(base2_y),
            color,
        )

        # Pulsing glow effect
        if (pyxel.frame_count // 8) % 2 == 0:
            pyxel.pset(round(tip_x), round(tip_y), 7)  # white tip flash

    # -----------------------------------------------------------------------
    # Footprints (legacy — kept if server sends them)
    # -----------------------------------------------------------------------

    def _draw_footprints(self, footprints: list) -> None:
        for fp in footprints:
            sx, sy = self._world_to_screen(fp.x, fp.y)
            if 0 <= sx < SCREEN_W and 0 <= sy < GAME_H:
                # Small diamond silhouette for thermal trace.
                pyxel.pset(sx,     sy - 2, C_FOOTPRINT)
                pyxel.pset(sx - 2, sy,     C_FOOTPRINT)
                pyxel.pset(sx + 2, sy,     C_FOOTPRINT)
                pyxel.pset(sx,     sy + 2, C_FOOTPRINT)

    # -----------------------------------------------------------------------
    # Players
    # -----------------------------------------------------------------------

    def _draw_other_players(self, snap: dict, local_id: str | None) -> None:
        interp_t = snap.get("interp_t", 1.0)
        prev_players = snap.get("prev_players", {})

        for pid, player in snap["players"].items():
            if pid == local_id:
                continue

            # Interpolate from previous position to current.
            prev = prev_players.get(pid)
            if prev is not None:
                draw_x = prev.x + (player.x - prev.x) * interp_t
                draw_y = prev.y + (player.y - prev.y) * interp_t
            else:
                draw_x, draw_y = player.x, player.y

            sx, sy = self._world_to_screen(draw_x, draw_y)
            if not (-8 <= sx < SCREEN_W + 8 and -8 <= sy < GAME_H + 8):
                continue
            revealed = player.revealed_role
            if revealed in ("PACMAN", "GHOST_BUILDER"):
                col = C_PAC_REV
            elif revealed and "GHOST" in revealed:
                col = C_GHOST_REV
            else:
                col = C_OTHER
            pyxel.circ(sx, sy, PLAYER_R, col)

    def _draw_self(
        self,
        px: float,
        py: float,
        role: str,
        stunned: bool,
        invisible: bool = False,
    ) -> None:
        sx, sy = self._world_to_screen(px, py)
        if not (0 <= sx < SCREEN_W and 0 <= sy < GAME_H):
            return

        # Invisible: blink slowly — visible only 1 frame every 4.
        if invisible:
            if (pyxel.frame_count // 6) % 4 != 0:
                # Still draw a faint outline so the player knows where they are.
                pyxel.circb(sx, sy, PLAYER_R, C_HUD_DIM)
                return
            # When visible frame: draw in dim color.
            col = C_HUD_DIM
        elif stunned:
            # Flash red/white during stun.
            col = C_STUN if (pyxel.frame_count // 4) % 2 == 0 else 7
        elif role in ("PACMAN", "GHOST_BUILDER"):
            col = C_SELF_PAC
        else:
            col = C_SELF

        # Filled circle.
        pyxel.circ(sx, sy, PLAYER_R, col)
        # Outer ring to distinguish from other players.
        pyxel.circb(sx, sy, PLAYER_R + 2, 7)

        # Role label above the sprite.
        label = {
            "PACMAN":         "PAC",
            "GHOST_TRACKER":  "TRK",
            "GHOST_BUILDER":  "BLD",
            "GHOST_SPRINTER": "SPR",
            "GHOST_TRAPPER":  "TRP",
            "GHOST_PHASER":   "PHS",
        }.get(role, "?")
        lx = sx - len(label) * 2
        pyxel.text(lx, sy - 10, label, col)

    # -----------------------------------------------------------------------
    # Death overlay
    # -----------------------------------------------------------------------

    def _draw_death_overlay(self, snap: dict) -> None:
        status = snap["status"]
        spectating = status.spectating_id

        # Draw a discrete black banner at the top of the screen (9 pixels high)
        pyxel.rect(0, 0, SCREEN_W, 9, 0)
        # Red line at the bottom of the banner
        pyxel.line(0, 9, SCREEN_W, 9, C_DEAD_TEXT)

        if spectating:
            text = f"DEAD - SPECTATING: {spectating[:12]} (SPACE to cycle)"
        else:
            text = "DEAD - No teammates alive"

        # Center the text in the banner
        self._ctext(text, SCREEN_W // 2, 2, C_DEAD_TEXT)

    # -----------------------------------------------------------------------
    # HUD
    # -----------------------------------------------------------------------

    def _draw_hud(self, status: GameStatus, snap: dict) -> None:
        hy = GAME_H  # HUD starts here
        pyxel.rect(0, hy, SCREEN_W, HUD_H, C_HUD_BG)
        pyxel.line(0, hy, SCREEN_W, hy, C_WALL)

        # Row 1: Score + Lives + Role
        pyxel.text(5, hy + 3, f"SCORE {status.score:06d}", C_HUD_TEXT)

        # Lives (hearts) — Pacman roles
        if status.role in ("PACMAN", "GHOST_BUILDER"):
            lives_x = 90
            for i in range(status.lives):
                hx = lives_x + i * 7
                if hx > 140:
                    break
                # Simple heart shape using pixels
                pyxel.pset(hx, hy + 4, C_LIVES)
                pyxel.pset(hx + 2, hy + 4, C_LIVES)
                pyxel.pset(hx - 1, hy + 5, C_LIVES)
                pyxel.pset(hx, hy + 5, C_LIVES)
                pyxel.pset(hx + 1, hy + 5, C_LIVES)
                pyxel.pset(hx + 2, hy + 5, C_LIVES)
                pyxel.pset(hx + 3, hy + 5, C_LIVES)
                pyxel.pset(hx, hy + 6, C_LIVES)
                pyxel.pset(hx + 1, hy + 6, C_LIVES)
                pyxel.pset(hx + 2, hy + 6, C_LIVES)
                pyxel.pset(hx + 1, hy + 7, C_LIVES)

        # Role label
        role_label = {
            "PACMAN":         "PACMAN",
            "GHOST_TRACKER":  "TRACKER",
            "GHOST_BUILDER":  "BUILDER",
            "GHOST_SPRINTER": "SPRINTER",
            "GHOST_TRAPPER":  "TRAPPER",
            "GHOST_PHASER":   "PHASER",
        }.get(status.role, status.role or "?")
        pyxel.text(5, hy + 12, role_label, C_SELF_PAC if status.role in ("PACMAN", "GHOST_BUILDER") else C_SELF)

        # Stun indicator (centred)
        if status.stunned:
            if (pyxel.frame_count // 6) % 2 == 0:
                self._ctext("!! STUNNED !!", SCREEN_W // 2, hy + 7, C_STUN)

        # Row 2: Invisibility charges (Pacman) + Ability cooldown

        # Invisibility charges — Pacman only
        if status.role == "PACMAN":
            inv_text = f"INVIS:{status.invis_charges}/{7}"
            inv_col = C_INVIS_TEXT if status.invis_charges > 0 else C_HUD_DIM
            pyxel.text(5, hy + 22, inv_text, inv_col)
            if status.invis_charges > 0:
                pyxel.text(52, hy + 22, "[E]", C_HUD_TEXT)

            # Active invisibility indicator
            if status.is_invisible:
                if (pyxel.frame_count // 8) % 2 == 0:
                    pyxel.text(70, hy + 22, "INVISIBLE", C_INVIS_TEXT)

        # Ability cooldown bar (right side)
        bar_x = SCREEN_W - 88
        bar_w = 80
        bar_y = hy + 3
        bar_h = 5
        pyxel.rect(bar_x, bar_y, bar_w, bar_h, C_WALL)

        if status.cooldown_ms > 0:
            # Estimate max cooldown from role.
            max_cd = {
                "GHOST_TRACKER":  60_000,
                "GHOST_BUILDER":  10_000,
                "GHOST_SPRINTER":  8_000,
                "GHOST_TRAPPER":  12_000,
                "GHOST_PHASER":   60_000,
            }.get(status.role, 10_000)
            filled = int(bar_w * (1.0 - status.cooldown_ms / max_cd))
            filled = max(0, min(bar_w, filled))
            pyxel.rect(bar_x, bar_y, filled, bar_h, 12)  # blue progress
            pyxel.text(bar_x, bar_y + 7, "ABILITY CD", C_HUD_DIM)
        else:
            pyxel.rect(bar_x, bar_y, bar_w, bar_h, C_READY)
            pyxel.text(bar_x + 12, bar_y + 7, "READY!", C_READY)

        # Mini controls reminder (very small, bottom-right corner)
        pyxel.text(SCREEN_W - 88, hy + 22, "ZQSD+SPACE", C_HUD_DIM)

        # Room ID (bottom, next to invisibility / roles)
        room_id = snap.get("room_id") or "…"
        pyxel.text(75, hy + 22, f"ROOM: {room_id}", C_HUD_DIM)

    # -----------------------------------------------------------------------
    # Game over screen
    # -----------------------------------------------------------------------

    def _draw_game_over(self, snap: dict) -> None:
        self._draw_title(SCREEN_W // 2, 14)

        room_id = snap.get("room_id") or "…"
        self._ctext(f"Room: {room_id}", SCREEN_W // 2, 26, C_HUD_DIM)

        winner = snap.get("winner", "?")
        w_col = C_SELF_PAC if winner == "PACMAN" else 12
        self._ctext(f"[ {winner} TEAM WINS ]", SCREEN_W // 2, 36, w_col)

        pyxel.line(10, 46, SCREEN_W - 10, 46, C_WALL)
        pyxel.text(10, 50, "FINAL SCORES", C_HUD_TEXT)

        scores = snap.get("scores", {})
        reveals = snap.get("reveals", {})
        local_id = snap.get("local_id")

        sorted_scores = sorted(scores.items(), key=lambda x: -x[1])
        for i, (pid, score) in enumerate(sorted_scores[:8]):
            y = 60 + i * 10
            role_str = reveals.get(pid, "?")[:8]
            is_self = pid == local_id
            prefix = "\x10 " if is_self else "  "
            col = C_SELF_PAC if is_self else C_OTHER
            pyxel.text(10, y, f"{prefix}{pid[:8]}  [{role_str}]  {score}", col)

        self._ctext("Press SPACE to return to lobby", SCREEN_W // 2, SCREEN_H - 24, C_HUD_TEXT)
        self._ctext("Press ESCAPE to quit", SCREEN_W // 2, SCREEN_H - 12, C_HUD_DIM)

    # -----------------------------------------------------------------------
    # Error overlay
    # -----------------------------------------------------------------------

    def _draw_error(self, error: str) -> None:
        msg = f"ERR: {error[:36]}"
        pyxel.rect(4, 4, SCREEN_W - 8, 11, C_STUN)
        pyxel.text(7, 7, msg, 7)

    # -----------------------------------------------------------------------
    # Helpers
    # -----------------------------------------------------------------------

    def _draw_title(self, cx: int, y: int) -> None:
        """Draw the animated game title."""
        title = "MULTIPACMAN"
        # Shadow
        self._ctext(title, cx + 1, y + 1, C_WALL)
        # Main (alternate orange/yellow for animation)
        col = C_TITLE if (pyxel.frame_count // 15) % 2 == 0 else 10
        self._ctext(title, cx, y, col)

    @staticmethod
    def _ctext(text: str, cx: int, y: int, col: int) -> None:
        """Draw text centred on cx."""
        pyxel.text(cx - len(text) * 2, y, text, col)
