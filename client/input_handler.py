"""
input_handler.py — Keyboard input capture and message queue.

Runs in the Pyxel thread (30 Hz). Maps keyboard state to direction vectors
and sends InputPayload messages to the network thread via the send callback.
"""
from __future__ import annotations

from typing import Callable

import pyxel

from state import GameState, GameStatus


class InputHandler:
    """Translates Pyxel key state into network messages."""

    def __init__(self, state: GameState, send_fn: Callable[[dict], None]) -> None:
        self.state = state
        self._send = send_fn
        self._seq: int = 0
        # Accumulated dash press: captured every frame (60fps) but only
        # sent on network frames (30Hz) so no btnp is ever dropped.
        self._pending_dash: bool = False
        # Accumulated invisibility press
        self._pending_invis: bool = False

    def update(self, snap: dict, should_send: bool = True) -> tuple[float, float]:
        """
        Called once per Pyxel frame (60 fps).
        Returns (dir_x, dir_y) for client-side prediction.
        should_send: if True, flush the pending input to the network.
        """
        room_state = snap.get("room_state", "LOBBY")

        if room_state == "LOBBY":
            self._handle_lobby(snap)  # lobby messages are event-driven; always check
            return 0.0, 0.0

        if room_state == "PLAYING":
            return self._handle_game(snap, should_send=should_send)

        if room_state == "FINISHED":
            self._handle_finished()

        return 0.0, 0.0

    # -----------------------------------------------------------------------
    # Phase-specific handlers
    # -----------------------------------------------------------------------

    def _handle_lobby(self, snap: dict) -> None:
        local_id = snap.get("local_id")

        # Toggle ready.
        if pyxel.btnp(pyxel.KEY_RETURN) or pyxel.btnp(pyxel.KEY_SPACE):
            self._send({"type": "READY"})

        # Force-start (any player with ≥ MinPlayers ready).
        if pyxel.btnp(pyxel.KEY_S):
            self._send({"type": "START_GAME"})

    def _handle_game(self, snap: dict, should_send: bool = True) -> tuple[float, float]:
        status = snap.get("status")

        # --- Dead: only SPACE to switch spectation target ---
        if status and status.is_dead:
            if pyxel.btnp(pyxel.KEY_SPACE):
                self._send({"type": "SPECTATE_NEXT"})
            return 0.0, 0.0

        # --- Builder mouse input handling ---
        if status and status.role == "GHOST_BUILDER":
            if status.stunned or status.cooldown_ms > 0:
                self.state.builder_step = 0
            else:
                self._handle_builder_clicks(status)

        dir_x, dir_y = 0.0, 0.0

        # Horizontal
        if pyxel.btn(pyxel.KEY_LEFT) or pyxel.btn(pyxel.KEY_A) or pyxel.btn(pyxel.KEY_Q):
            dir_x = -1.0
        elif pyxel.btn(pyxel.KEY_RIGHT) or pyxel.btn(pyxel.KEY_D):
            dir_x = 1.0

        # Vertical
        if pyxel.btn(pyxel.KEY_UP) or pyxel.btn(pyxel.KEY_W) or pyxel.btn(pyxel.KEY_Z):
            dir_y = -1.0
        elif pyxel.btn(pyxel.KEY_DOWN) or pyxel.btn(pyxel.KEY_S):
            dir_y = 1.0

        # Normalise diagonal movement (avoids faster diagonal speed).
        if dir_x != 0.0 and dir_y != 0.0:
            factor = 0.7071  # 1 / sqrt(2)
            dir_x *= factor
            dir_y *= factor

        # Accumulate dash every frame (btnp fires once per keypress regardless of fps).
        if pyxel.btnp(pyxel.KEY_SPACE):
            self._pending_dash = True

        # Accumulate invisibility activation (E key).
        if pyxel.btnp(pyxel.KEY_E):
            self._pending_invis = True

        # Send input to network at 30 Hz (every other frame).
        if should_send:
            dash = self._pending_dash
            self._pending_dash = False  # consume

            # Send invisibility as a separate message.
            if self._pending_invis:
                self._pending_invis = False
                self._send({"type": "USE_INVIS"})

            self._seq += 1
            self._send({
                "type": "INPUT",
                "seq": self._seq,
                "dir_x": round(dir_x, 4),
                "dir_y": round(dir_y, 4),
                "dash": dash,
            })
            
            # Record sent inputs for server reconciliation
            self.state.pending_inputs.append({
                "seq": self._seq,
                "dir_x": round(dir_x, 4),
                "dir_y": round(dir_y, 4),
                "dash": dash,
            })
            
            if dash and snap.get("status"):
                role = snap["status"].role
                if role == "GHOST_SPRINTER":
                    self.state.status.is_dashing = True
                    self.state.status.dash_remaining_ticks = 9
                    length = (dir_x * dir_x + dir_y * dir_y) ** 0.5
                    if length > 0:
                        self.state.dash_dir_x = dir_x / length
                        self.state.dash_dir_y = dir_y / length
                elif role == "GHOST_PHASER":
                    self.state.status.is_phasing = True
                    self.state.status.phasing_remaining_ticks = 600

        return dir_x, dir_y

    def _handle_finished(self) -> None:
        if pyxel.btnp(pyxel.KEY_SPACE) or pyxel.btnp(pyxel.KEY_RETURN):
            self._send({"type": "READY"})

    def _handle_builder_clicks(self, status: GameStatus) -> None:
        # Cancel build selection on right click
        if pyxel.btnp(pyxel.MOUSE_BUTTON_RIGHT):
            self.state.builder_step = 0
            return

        if pyxel.btnp(pyxel.MOUSE_BUTTON_LEFT):
            # Calculate world grid coordinates adjusted by the camera position
            grid_x = int((pyxel.mouse_x + self.state.camera_x) // 8)
            grid_y = int((pyxel.mouse_y + self.state.camera_y) // 8)

            # Check map bounds
            if grid_x < 0 or grid_x >= self.state.map_width or grid_y < 0 or grid_y >= self.state.map_height:
                return

            if self.state.builder_step == 0:
                # Target tile must be empty or pellet (TileEmpty = 0, TilePellet = 2)
                tile_type = self.state.tile_cache.get((grid_x, grid_y))
                if tile_type not in (0, 2):
                    return # Must start building on an empty/pellet tile

                self.state.builder_x1 = grid_x
                self.state.builder_y1 = grid_y
                self.state.builder_step = 1
            elif self.state.builder_step == 1:
                dx = grid_x - self.state.builder_x1
                dy = grid_y - self.state.builder_y1

                # Clicked same tile -> cancel selection
                if dx == 0 and dy == 0:
                    self.state.builder_step = 0
                    return

                # Calculate strictly adjacent coordinates
                if abs(dx) >= abs(dy):
                    x2 = self.state.builder_x1 + (1 if dx >= 0 else -1)
                    y2 = self.state.builder_y1
                else:
                    x2 = self.state.builder_x1
                    y2 = self.state.builder_y1 + (1 if dy >= 0 else -1)

                # Ensure second tile is empty (0), permanent wall (1), or pellet (2)
                tile_type2 = self.state.tile_cache.get((x2, y2))
                if tile_type2 not in (0, 1, 2):
                    return

                # Send build message to server
                self._send({
                    "type": "BUILD",
                    "coords": [self.state.builder_x1, self.state.builder_y1, x2, y2]
                })
                self.state.builder_step = 0
