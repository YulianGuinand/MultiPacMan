"""
network.py — WebSocket client running in a dedicated daemon thread.

Architecture:
    - One daemon thread owns an asyncio event loop.
    - Incoming messages are dispatched to GameState (thread-safe).
    - Outgoing messages are pushed via a thread-safe queue from the Pyxel thread.
    - Automatic reconnection with exponential back-off (1 s → 30 s).
"""
from __future__ import annotations

import asyncio
import json
import logging
import threading
from typing import Optional

import websockets
import websockets.exceptions

import socket
from urllib.parse import urlparse

from state import GameState

logger = logging.getLogger(__name__)


class NetworkManager:
    """Manages the WebSocket connection in a background daemon thread."""

    def __init__(
        self,
        state: GameState,
        server_url: str = "wss://pacman.yulian-server.duckdns.org/ws",
    ) -> None:
        self.state = state
        self.server_url = server_url

        self._loop: Optional[asyncio.AbstractEventLoop] = None
        self._send_queue: Optional[asyncio.Queue] = None
        self._thread: Optional[threading.Thread] = None
        self._running = False
        self._udp_sock: Optional[socket.socket] = None
        self.server_host = ""
        self.udp_confirmed = False

    # -----------------------------------------------------------------------
    # Public API (Pyxel thread)
    # -----------------------------------------------------------------------

    def start(self, room_id: str = "default") -> None:
        """Start the network daemon thread."""
        self._running = True
        url = f"{self.server_url}?room={room_id}"
        self._thread = threading.Thread(
            target=self._run_loop,
            args=(url,),
            daemon=True,
            name="NetworkThread",
        )
        self._thread.start()

    def stop(self) -> None:
        """Signal the network thread to shut down."""
        self._running = False
        if self._loop and self._send_queue is not None:
            self._loop.call_soon_threadsafe(self._send_queue.put_nowait, None)

    def send(self, msg: dict) -> None:
        """Enqueue a message to be sent over WebSocket (thread-safe)."""
        if self._loop and self._send_queue is not None:
            try:
                self._loop.call_soon_threadsafe(self._send_queue.put_nowait, msg)
            except RuntimeError as exc:
                logger.error("Failed to enqueue message (event loop closed): %s", exc)

    # -----------------------------------------------------------------------
    # Internal — network thread
    # -----------------------------------------------------------------------

    def _run_loop(self, url: str) -> None:
        parsed = urlparse(url)
        self.server_host = parsed.hostname or "127.0.0.1"
        if self.server_host == "localhost":
            self.server_host = "127.0.0.1"

        self._udp_sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self._udp_sock.bind(("", 0))

        self._loop = asyncio.new_event_loop()
        asyncio.set_event_loop(self._loop)

        # Run incoming UDP packet reader as background task
        self._loop.create_task(self._recv_udp_loop())

        try:
            self._loop.run_until_complete(self._connect(url))
        except Exception as exc:
            logger.error("Network loop fatal error: %s", exc)
            self.state.set_error(str(exc))
        finally:
            if self._udp_sock:
                self._udp_sock.close()
                self._udp_sock = None
            self._loop.close()

    async def _connect(self, url: str) -> None:
        self._send_queue = asyncio.Queue()
        delay = 1.0

        while self._running:
            self.udp_confirmed = False
            try:
                logger.info("Connecting to %s …", url)
                async with websockets.connect(
                    url,
                    ping_interval=20,
                    ping_timeout=30,
                    open_timeout=10,
                ) as ws:
                    delay = 1.0  # Reset back-off on success.
                    logger.info("Connected!")
                    # Run recv and send loops concurrently.
                    await asyncio.gather(
                        self._recv_loop(ws),
                        self._send_loop(ws),
                    )
            except (
                websockets.exceptions.ConnectionClosed,
                websockets.exceptions.WebSocketException,
                OSError,
                asyncio.TimeoutError,
            ) as exc:
                logger.warning("Connection lost (%s). Reconnecting in %.0f s…", exc, delay)
                self.state.connected = False
                if self._running:
                    await asyncio.sleep(delay)
                    delay = min(delay * 2, 30.0)

    async def _recv_loop(self, ws) -> None:
        async for raw in ws:
            try:
                data = json.loads(raw)
                self._dispatch(data)
            except json.JSONDecodeError as exc:
                logger.error("JSON decode error: %s", exc)

    async def _send_loop(self, ws) -> None:
        while True:
            msg = await self._send_queue.get()
            if msg is None:
                # Shutdown sentinel.
                break
            try:
                is_input = (msg.get("type") == "INPUT")
                sent_udp = False
                if is_input and self.state.udp_port > 0 and self.state.local_id and self._udp_sock:
                    try:
                        udp_payload = {
                            "client_id": self.state.local_id,
                            "seq": msg.get("seq"),
                            "dir_x": msg.get("dir_x"),
                            "dir_y": msg.get("dir_y"),
                            "dash": msg.get("dash", False),
                        }
                        if udp_payload["dir_x"] != 0.0 or udp_payload["dir_y"] != 0.0:
                            logger.debug("Sending non-zero UDP input: %s", udp_payload)
                        data = json.dumps(udp_payload).encode("utf-8")
                        self._udp_sock.sendto(data, (self.server_host, self.state.udp_port))
                        sent_udp = True
                    except Exception as e:
                        logger.debug("Failed sending UDP input: %s", e)
                
                # Always send to WebSocket for 100% reliability
                await ws.send(json.dumps(msg))
                if is_input and (msg.get("dir_x") != 0.0 or msg.get("dir_y") != 0.0):
                    logger.debug("Sending non-zero TCP input: %s", msg)
            except Exception as exc:
                logger.error("Send error: %s", exc)
                break

    async def _recv_udp_loop(self) -> None:
        loop = asyncio.get_running_loop()
        while self._running:
            if not self._udp_sock:
                await asyncio.sleep(0.1)
                continue
            try:
                data, addr = await loop.run_in_executor(None, self._udp_sock.recvfrom, 65535)
                if data:
                    # Drain any pending UDP packets from the OS buffer to keep only the newest one
                    last_data = data
                    while self._running:
                        try:
                            self._udp_sock.setblocking(False)
                            next_data, next_addr = self._udp_sock.recvfrom(65535)
                            if next_data:
                                last_data = next_data
                        except BlockingIOError:
                            break
                        finally:
                            self._udp_sock.setblocking(True)

                    try:
                        payload = json.loads(last_data.decode("utf-8"))
                        if payload.get("type") == "GAME_STATE":
                            self.state.update_game_state(payload)
                            if not self.udp_confirmed:
                                self.udp_confirmed = True
                                logger.info("UDP connection confirmed by receiving GAME_STATE!")
                                self.send({"type": "CONFIRM_UDP"})
                    except Exception as e:
                        logger.error("Error parsing UDP game state: %s", e)
            except Exception as exc:
                if self._running:
                    logger.debug("UDP receive error: %s", exc)
                await asyncio.sleep(0.01)

    def _dispatch(self, data: dict) -> None:
        msg_type = data.get("type")
        dispatch = {
            "WELCOME":      self.state.update_welcome,
            "LOBBY_UPDATE": self.state.update_lobby,
            "GAME_START":   self.state.update_game_start,
            "GAME_STATE":   self.state.update_game_state,
            "GAME_OVER":    self.state.update_game_over,
        }
        handler = dispatch.get(msg_type)
        if handler:
            handler(data)
        elif msg_type == "ERROR":
            self.state.set_error(data.get("message", "Unknown server error"))
        else:
            logger.debug("Unknown message type: %s", msg_type)
