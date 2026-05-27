"""
input_handler.py — Keyboard input capture and message queue.

Runs in the Pyxel thread (30 Hz). Maps keyboard state to direction vectors
and sends InputPayload messages to the network thread via the send callback.
"""
from __future__ import annotations

from typing import Callable

import pyxel


class InputHandler:
    """Translates Pyxel key state into network messages."""

    def __init__(self, send_fn: Callable[[dict], None]) -> None:
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

        dir_x, dir_y = 0.0, 0.0

        # Horizontal
        if pyxel.btn(pyxel.KEY_LEFT) or pyxel.btn(pyxel.KEY_A):
            dir_x = -1.0
        elif pyxel.btn(pyxel.KEY_RIGHT) or pyxel.btn(pyxel.KEY_D):
            dir_x = 1.0

        # Vertical
        if pyxel.btn(pyxel.KEY_UP) or pyxel.btn(pyxel.KEY_W):
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

        return dir_x, dir_y

    def _handle_finished(self) -> None:
        # Placeholder: could reconnect or return to lobby.
        pass
