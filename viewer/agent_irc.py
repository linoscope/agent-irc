"""
agent_irc.py — minimal IRC client for agents.

Distilled from chapters 01-03 of the agent-irc tutorial. Stdlib only.
Not for production: no TLS, no IRCv3 caps beyond what the viewer needs,
no reconnection logic. Designed to be readable in one sitting.

Usage:

    from agent_irc import IRCAgent

    with IRCAgent("localhost", 17000, nick="alice-bot") as agent:
        agent.join("#room")
        agent.send_message("#room", "hello!")
        for msg in agent.messages(timeout=10):
            if msg.target == "#room":
                print(f"<{msg.from_nick}> {msg.text}")
                if msg.from_nick != agent.nick:
                    agent.send_message("#room", f"hi {msg.from_nick}!")

Opt-in features:
  - log_path=...       : append every wire event as JSON to this file
  - cap_request=[...]  : ask the server for IRCv3 capabilities (e.g. ['account-tag'])
  - on the wire, server-initiated PINGs are answered automatically
"""
from __future__ import annotations

import json
import socket
import threading
import time
from dataclasses import dataclass, field
from queue import Empty, Queue
from typing import Iterator, Optional


@dataclass
class Message:
    """One parsed PRIVMSG event delivered to the agent's `messages()` loop."""
    from_nick: str
    target: str       # channel name (#room) or this agent's nick (DM)
    text: str
    raw: str          # the full original line
    tags: dict = field(default_factory=dict)  # IRCv3 tags, if requested via caps

    @property
    def is_channel(self) -> bool:
        return self.target.startswith("#")

    @property
    def account(self) -> Optional[str]:
        """The verified account name, or None if not authenticated / cap not requested."""
        return self.tags.get("account")


class IRCAgent:
    def __init__(
        self,
        host: str,
        port: int,
        nick: str,
        realname: Optional[str] = None,
        log_path: Optional[str] = None,
        cap_request: Optional[list[str]] = None,
    ):
        self.host = host
        self.port = port
        self.nick = nick
        self.realname = realname or f"{nick} the agent"
        self.log_path = log_path
        self.cap_request = cap_request or []
        self._sock: Optional[socket.socket] = None
        self._reader: Optional[threading.Thread] = None
        self._inbox: "Queue[Message]" = Queue()
        self._send_lock = threading.Lock()
        self._closed = threading.Event()
        self._registered = threading.Event()
        # CHATHISTORY responses are routed through a separate channel. The
        # `_history_collecting` flag is set while we're actively collecting
        # a batch for a `chathistory()` call; messages tagged with batch=...
        # land in `_history_buffer` instead of the main inbox.
        self._history_buffer: "list[Message]" = []
        self._history_collecting = False
        self._history_done = threading.Event()

    # ---- lifecycle -------------------------------------------------------

    def connect(self, timeout: float = 5.0) -> "IRCAgent":
        """Open the TCP connection and complete the registration handshake."""
        self._sock = socket.create_connection((self.host, self.port), timeout=timeout)
        self._sock.settimeout(None)
        self._reader = threading.Thread(target=self._read_loop, daemon=True)
        self._reader.start()

        if self.cap_request:
            self._send_raw("CAP LS 302")
            self._send_raw(f"NICK {self.nick}")
            self._send_raw(f"USER {self.nick} 0 * :{self.realname}")
            self._send_raw("CAP REQ :" + " ".join(self.cap_request))
            self._send_raw("CAP END")
        else:
            self._send_raw(f"NICK {self.nick}")
            self._send_raw(f"USER {self.nick} 0 * :{self.realname}")

        if not self._registered.wait(timeout=timeout):
            raise TimeoutError("did not receive 001 RPL_WELCOME within timeout")
        return self

    def __enter__(self):
        return self.connect()

    def __exit__(self, *exc):
        self.close()

    def close(self):
        if self._sock is None:
            return
        try:
            self._send_raw("QUIT :bye")
        except Exception:
            pass
        self._closed.set()
        try:
            self._sock.close()
        except Exception:
            pass
        self._sock = None

    # ---- commands --------------------------------------------------------

    def join(self, channel: str) -> None:
        self._send_raw(f"JOIN {channel}")

    def part(self, channel: str, reason: str = "") -> None:
        self._send_raw(f"PART {channel} :{reason}")

    def send_message(self, target: str, text: str) -> None:
        # Sanitize: \r and \n in agent-emitted text would let one logical
        # message inject additional IRC commands. Strip them.
        text = text.replace("\r", " ").replace("\n", " ")
        self._send_raw(f"PRIVMSG {target} :{text}")

    def raw(self, line: str) -> None:
        """Escape hatch: send any IRC line. Used by the viewer for CHATHISTORY."""
        self._send_raw(line)

    def chathistory(self, target: str, count: int = 100, timeout: float = 5.0) -> "list[Message]":
        """Ask the server for the last `count` messages in `target`.

        Requires the server to support draft/chathistory and the connecting
        account to have history-read privileges (Ergo grants this to channel
        members and account owners).

        This is a simplified blocking implementation: only one chathistory call
        in flight at a time. For tutorial purposes, that's enough.
        """
        self._history_buffer = []
        self._history_collecting = True
        self._history_done.clear()
        self._send_raw(f"CHATHISTORY LATEST {target} * {count}")
        self._history_done.wait(timeout=timeout)
        self._history_collecting = False
        return list(self._history_buffer)

    def messages(self, timeout: Optional[float] = None) -> Iterator[Message]:
        """Yield incoming PRIVMSGs. Returns when timeout elapses without one."""
        while not self._closed.is_set():
            try:
                msg = self._inbox.get(timeout=timeout)
            except Empty:
                return
            yield msg

    # ---- internals -------------------------------------------------------

    def _send_raw(self, line: str) -> None:
        if self._sock is None:
            raise RuntimeError("not connected")
        with self._send_lock:
            self._sock.sendall((line + "\r\n").encode("utf-8", errors="replace"))
        self._log_event("sent", line)

    def _read_loop(self) -> None:
        buf = b""
        while not self._closed.is_set():
            try:
                chunk = self._sock.recv(4096)
            except OSError:
                break
            if not chunk:
                break
            buf += chunk
            while b"\r\n" in buf:
                line_bytes, buf = buf.split(b"\r\n", 1)
                if line_bytes:
                    self._handle(line_bytes.decode("utf-8", errors="replace"))
        self._closed.set()
        # Unblock any pending chathistory or messages() callers.
        self._history_done.set()

    def _handle(self, line: str) -> None:
        self._log_event("recv", line)

        # Parse: [@tags] [:source] verb [params...] [:trailing]
        tags: dict[str, str] = {}
        if line.startswith("@"):
            tag_block, _, line = line[1:].partition(" ")
            for kv in tag_block.split(";"):
                if not kv:
                    continue
                k, _, v = kv.partition("=")
                tags[k] = _unescape_tag(v)

        source = None
        if line.startswith(":"):
            source, _, line = line[1:].partition(" ")

        trailing = None
        if " :" in line:
            line, trailing = line.split(" :", 1)
        parts = line.split()
        if not parts:
            return
        verb = parts[0]
        params = parts[1:]
        if trailing is not None:
            params.append(trailing)

        verb_upper = verb.upper()

        # PING — answer immediately, never appears in inbox.
        if verb_upper == "PING":
            if params:
                self._send_raw(f"PONG :{params[0]}")
            return

        # 001 RPL_WELCOME — registration completed.
        if verb == "001":
            self._registered.set()
            return

        # CHATHISTORY responses arrive as inner messages of a BATCH.
        # We don't bother parsing batch boundaries — just route any message
        # carrying a `batch=` tag into the history buffer while collecting,
        # and watch for the BATCH end marker.
        if verb_upper == "BATCH" and self._history_collecting:
            # ":server BATCH +tag chathistory #channel" or ":server BATCH -tag"
            if params and params[0].startswith("-"):
                self._history_done.set()
            return

        # PRIVMSG dispatch.
        if verb_upper == "PRIVMSG" and len(params) >= 2 and source:
            from_nick = source.split("!", 1)[0]
            msg = Message(
                from_nick=from_nick,
                target=params[0],
                text=params[1],
                raw=line,
                tags=tags,
            )
            if self._history_collecting and "batch" in tags:
                self._history_buffer.append(msg)
            else:
                self._inbox.put(msg)
            return

    def _log_event(self, direction: str, line: str) -> None:
        if not self.log_path:
            return
        rec = {
            "t": time.time(),
            "nick": self.nick,
            "dir": direction,
            "line": line,
        }
        try:
            with open(self.log_path, "a", encoding="utf-8") as f:
                f.write(json.dumps(rec) + "\n")
        except OSError:
            pass


def _unescape_tag(v: str) -> str:
    """Decode IRCv3 tag-value escape sequence: \\: → ;, \\s → space, etc."""
    if "\\" not in v:
        return v
    out: list[str] = []
    i = 0
    while i < len(v):
        if v[i] != "\\" or i + 1 >= len(v):
            out.append(v[i])
            i += 1
            continue
        nxt = v[i + 1]
        out.append(
            {":": ";", "s": " ", "\\": "\\", "r": "\r", "n": "\n"}.get(nxt, nxt)
        )
        i += 2
    return "".join(out)
