"""
Playwright UI test — CLI flow.

Parallel to test_viewer.py but the live message is injected via the agent-irc
*Go CLI* instead of the Python agent_irc.py library. Confirms that wire
output produced by the CLI flows through Ergo and into the viewer's SSE
stream identically to the Python path.

Run after `appendix-a-agent-client/start-ergo.sh` and `start-viewer.sh`.
Requires the agent-irc binary at /tmp/agent-irc (or AGENT_IRC_BIN env var).
"""
from __future__ import annotations

import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from playwright.sync_api import sync_playwright, expect  # noqa: E402


VIEWER_URL = os.environ.get("VIEWER_URL", "http://localhost:8080")
AGENT_IRC = os.environ.get("AGENT_IRC_BIN", "/tmp/agent-irc")
SCREENSHOT = Path(__file__).resolve().parent.parent / "screenshots" / "viewer-channel-cli.png"


def run_cli(*args: str, timeout: int = 5) -> str:
    """Invoke agent-irc CLI; return stdout."""
    res = subprocess.run(
        [AGENT_IRC, *args],
        capture_output=True, text=True, timeout=timeout,
    )
    if res.returncode != 0:
        raise RuntimeError(f"{AGENT_IRC} {' '.join(args)} → {res.returncode}: {res.stderr}")
    return res.stdout.strip()


def main() -> int:
    if not shutil.which(AGENT_IRC) and not Path(AGENT_IRC).exists():
        print(f"FAIL: agent-irc binary not at {AGENT_IRC}", file=sys.stderr)
        return 1

    SCREENSHOT.parent.mkdir(parents=True, exist_ok=True)

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        ctx = browser.new_context(viewport={"width": 1100, "height": 720})
        page = ctx.new_page()

        # Open the channel page first so the SSE stream is connected before we
        # send anything.
        page.goto(f"{VIEWER_URL}/c/agents-room")
        page.wait_for_function(
            "document.getElementById('status').textContent.includes('live')",
            timeout=5000,
        )
        print("[playwright-cli] ✓ channel page loaded, SSE connected", flush=True)

        # Bring up the CLI agent and send a marker message.
        marker = f"cli-live-{int(time.time())}"
        run_cli("connect", "localhost:17000", "--nick", "cli-probe")
        try:
            run_cli("join", "#agents-room", "--nick", "cli-probe")
            time.sleep(0.4)
            run_cli("send", "#agents-room", "--nick", "cli-probe", marker)
            print(f"[playwright-cli] sent via CLI: {marker}", flush=True)

            # The page should append the live event.
            page.wait_for_selector(f'.text:has-text("{marker}")', timeout=5000)
            print(f"[playwright-cli] ✓ {marker!r} rendered via SSE", flush=True)

            page.screenshot(path=str(SCREENSHOT), full_page=True)
            print(f"[playwright-cli] ✓ screenshot → {SCREENSHOT}", flush=True)
        finally:
            try:
                run_cli("quit", "--nick", "cli-probe")
            except Exception:
                pass

        browser.close()

    print("PASS: viewer renders CLI-sent messages live", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
