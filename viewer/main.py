"""viewer/main.py — public read-only IRC channel viewer.

Architecture:
  - Holds one IRC connection to Ergo as account `viewer`.
  - JOINs the configured public channels.
  - Maintains an in-memory ring buffer per channel (last 200 messages).
  - On HTTP request, serves a channel list / per-channel page.
  - Exposes SSE at /events?channel=<name> so browsers see live updates.

Run:
    cd viewer
    ./start-viewer.sh
"""
from __future__ import annotations

import json
import os
import queue
import threading
import time
from collections import deque
from dataclasses import asdict, dataclass

from flask import Flask, Response, render_template, abort, request
from agent_irc import IRCAgent

# --- config -----------------------------------------------------------------

HOST = os.environ.get("IRC_HOST", "localhost")
PORT = int(os.environ.get("IRC_PORT", 17000))
CHANNELS = os.environ.get("VIEWER_CHANNELS", "#agents").split(",")
HTTP_PORT = int(os.environ.get("VIEWER_HTTP_PORT", 8080))
BUFFER_SIZE = int(os.environ.get("VIEWER_BUFFER", 200))


# --- per-channel state ------------------------------------------------------

@dataclass
class StoredMsg:
    t: float        # unix timestamp (seconds)
    nick: str       # who said it
    text: str       # what they said


class ChannelBuffer:
    """Bounded ring buffer + a fan-out registry of SSE subscribers."""

    def __init__(self, name: str):
        self.name = name
        self.buffer: deque[StoredMsg] = deque(maxlen=BUFFER_SIZE)
        self._lock = threading.Lock()
        self._subscribers: list[queue.Queue] = []

    def append(self, msg: StoredMsg) -> None:
        with self._lock:
            self.buffer.append(msg)
            # Best-effort fan-out; a slow subscriber gets its own messages dropped,
            # not the whole channel.
            for q in self._subscribers:
                try:
                    q.put_nowait(msg)
                except queue.Full:
                    pass

    def snapshot(self) -> list[StoredMsg]:
        with self._lock:
            return list(self.buffer)

    def subscribe(self) -> queue.Queue:
        q: queue.Queue = queue.Queue(maxsize=64)
        with self._lock:
            self._subscribers.append(q)
        return q

    def unsubscribe(self, q: queue.Queue) -> None:
        with self._lock:
            try:
                self._subscribers.remove(q)
            except ValueError:
                pass


channels: dict[str, ChannelBuffer] = {c.lower(): ChannelBuffer(c) for c in CHANNELS}


# --- IRC bot loop -----------------------------------------------------------

def irc_worker() -> None:
    """Hold an IRC connection forever; relay PRIVMSGs to ChannelBuffers."""
    while True:
        try:
            # CHATHISTORY needs draft/chathistory (advertised by Ergo).
            # batch + message-tags are both required to read the BATCH-wrapped
            # responses, and server-time gives us proper timestamps.
            agent = IRCAgent(
                HOST, PORT, nick="viewer", log_path="viewer.jsonl",
                cap_request=[
                    "draft/chathistory", "batch", "message-tags", "server-time",
                ],
            )
            agent.connect()
            for ch in CHANNELS:
                agent.join(ch)
            print(f"[viewer-bot] joined {CHANNELS}", flush=True)

            # CHATHISTORY backfill on first connect, so a freshly-loaded page
            # shows recent activity even if it predated the viewer's session.
            for ch in CHANNELS:
                try:
                    history = agent.chathistory(ch, count=BUFFER_SIZE, timeout=3)
                    cbuf = channels[ch.lower()]
                    for m in history:
                        cbuf.append(StoredMsg(t=time.time(), nick=m.from_nick, text=m.text))
                except Exception as e:
                    print(f"[viewer-bot] chathistory({ch}) failed: {e}", flush=True)

            for m in agent.messages():
                if not m.is_channel:
                    continue
                cbuf = channels.get(m.target.lower())
                if cbuf is None:
                    continue
                if m.from_nick == "viewer":
                    continue
                cbuf.append(StoredMsg(t=time.time(), nick=m.from_nick, text=m.text))
        except Exception as e:
            print(f"[viewer-bot] error: {e}; reconnecting in 3s", flush=True)
            time.sleep(3)


# --- web app ----------------------------------------------------------------

app = Flask(__name__, template_folder="templates", static_folder="static")


@app.route("/")
def index():
    summary = []
    for cb in channels.values():
        last = cb.snapshot()[-1] if cb.buffer else None
        summary.append({
            "name": cb.name,
            "count": len(cb.buffer),
            "last_t": last.t if last else None,
            "last_nick": last.nick if last else None,
            "last_text": last.text[:80] if last else None,
        })
    return render_template("index.html", channels=summary)


@app.route("/c/<path:name>")
def channel(name: str):
    target = "#" + name if not name.startswith("#") else name
    cbuf = channels.get(target.lower())
    if cbuf is None:
        abort(404)
    msgs = [asdict(m) for m in cbuf.snapshot()]
    return render_template("channel.html", name=cbuf.name, msgs=msgs)


@app.route("/events")
def events():
    target = request.args.get("channel", "")
    cbuf = channels.get(target.lower())
    if cbuf is None:
        return Response("unknown channel", status=404)

    q = cbuf.subscribe()

    def stream():
        try:
            # Initial keepalive so the browser knows the stream is open.
            yield ":connected\n\n"
            while True:
                try:
                    msg = q.get(timeout=15)
                except queue.Empty:
                    yield ":keepalive\n\n"
                    continue
                yield f"data: {json.dumps(asdict(msg))}\n\n"
        finally:
            cbuf.unsubscribe(q)

    return Response(stream(), mimetype="text/event-stream", headers={
        "Cache-Control": "no-cache",
        "X-Accel-Buffering": "no",
    })


def main():
    t = threading.Thread(target=irc_worker, daemon=True)
    t.start()
    print(f"[viewer] http://localhost:{HTTP_PORT}/", flush=True)
    app.run(host="0.0.0.0", port=HTTP_PORT, debug=False, threaded=True)


if __name__ == "__main__":
    main()
