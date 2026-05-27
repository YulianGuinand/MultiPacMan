"""
main.py — MultiPacMan client entry point.

Usage:
    uv run python main.py [--server wss://host:port/ws] [--room <room_id>]

Architecture:
    Pyxel (60 fps render) ──► InputHandler ──► NetworkManager.send()  [30 Hz]
                        ◄──── Renderer.draw() ◄─── GameState.get_snapshot()
                                                         ▲
                                             NetworkThread (asyncio)
"""
from __future__ import annotations

import argparse
import logging

import pyxel

from state import GameState
from network import NetworkManager
from input_handler import InputHandler
from renderer import Renderer, SCREEN_W, SCREEN_H

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(name)-16s] %(levelname)s: %(message)s",
    datefmt="%H:%M:%S",
)

# Render at 60 fps; send inputs to the server at 30 Hz (every other frame).
# Prediction speed = server_speed_tiles_per_tick / 2  (half per frame at 60fps
# vs. the server tick rate of 30Hz, so real-world tiles/sec stays identical).
_FPS = 60
_SEND_EVERY = 2  # frames between network sends  (60 / 2 = 30 Hz)

_PREDICT_SPEED: dict[str, float] = {
    "PACMAN":         0.075,  # 0.150 / 2
    "GHOST_TRACKER":  0.055,  # 0.110 / 2
    "GHOST_BUILDER":  0.054,  # 0.107 / 2
    "GHOST_SPRINTER": 0.067,  # 0.133 / 2
}
_DEFAULT_SPEED = 0.065  # 0.130 / 2


class App:
    """Main application: wires together Pyxel, GameState, Network, and Renderer."""

    def __init__(self, server_url: str, room_id: str) -> None:
        self.state = GameState()
        self.network = NetworkManager(self.state, server_url)
        self.renderer = Renderer(self.state)
        self.input_handler = InputHandler(self.state, self.network.send)
        self._frame: int = 0  # tracks frames for send-rate control

        # Start WebSocket thread before Pyxel (Pyxel blocks on run()).
        self.network.start(room_id)

        pyxel.init(SCREEN_W, SCREEN_H, title="MultiPacMan", fps=_FPS)

        # Load resources and play music 1 in a loop
        import os
        try:
            res_path = os.path.join(os.path.dirname(__file__), "my_resource.pyxres")
            if os.path.exists(res_path):
                pyxel.load(res_path)
                pyxel.playm(1, loop=True)
            else:
                logging.warning(f"Resource file not found at: {res_path}")
        except Exception as e:
            logging.error(f"Failed to load my_resource.pyxres or play music: {e}")

        pyxel.run(self.update, self.draw)

    # -----------------------------------------------------------------------
    # Pyxel callbacks (60 fps render / 30 Hz network)
    # -----------------------------------------------------------------------

    def update(self) -> None:
        # Global quit.
        if pyxel.btnp(pyxel.KEY_ESCAPE):
            self.network.stop()
            pyxel.quit()
            return

        self._frame += 1
        # Send network inputs at 30 Hz (every _SEND_EVERY frames).
        should_send = (self._frame % _SEND_EVERY == 0)

        snap = self.state.get_snapshot()

        # Process keyboard → conditionally send to network.
        dir_x, dir_y = self.input_handler.update(snap, should_send=should_send)

        # Client-side prediction at 60 fps: move local sprite every frame
        # without waiting for server confirmation.
        if snap["room_state"] == "PLAYING" and (dir_x != 0.0 or dir_y != 0.0):
            speed = _PREDICT_SPEED.get(snap["status"].role, _DEFAULT_SPEED)
            self.state.apply_local_prediction(dir_x, dir_y, speed)

    def draw(self) -> None:
        self.renderer.draw()


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="MultiPacMan — asymmetric multiplayer Pac-Man client",
    )
    parser.add_argument(
        "--server",
        default="wss://pacman.yulian-server.duckdns.org/ws",
        metavar="URL",
        help="WebSocket server URL (default: wss://pacman.yulian-server.duckdns.org/ws)",
    )
    parser.add_argument(
        "--room",
        default="default",
        metavar="ID",
        help="Room ID to join (default: 'default')",
    )
    return parser.parse_args()


if __name__ == "__main__":
    args = parse_args()
    App(server_url=args.server, room_id=args.room)
