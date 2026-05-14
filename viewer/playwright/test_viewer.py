"""
Playwright UI test for the public viewer.

Exercises:
  1. Index page renders and lists the configured channel(s).
  2. Channel detail page renders and the SSE stream connects.
  3. A live IRC message lands in the DOM via SSE within 5s.
  4. Capture screenshots of the index and channel pages for the README.

Run after `start-ergo.sh` and `start-viewer.sh` are up, and the agent-irc Go
binary is on disk at /tmp/agent-irc (or AGENT_IRC_BIN env var).
"""
from __future__ import annotations

import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

from playwright.sync_api import sync_playwright, expect


VIEWER_URL = os.environ.get("VIEWER_URL", "http://localhost:8080")
AGENT_IRC = os.environ.get("AGENT_IRC_BIN", "/tmp/agent-irc")
CHANNEL = os.environ.get("VIEWER_TEST_CHANNEL", "#agents")
SCREENSHOTS = Path(__file__).resolve().parent.parent / "screenshots"
SCREENSHOT_INDEX = str(SCREENSHOTS / "viewer-index.png")
SCREENSHOT_CHANNEL = str(SCREENSHOTS / "viewer-channel.png")


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

    SCREENSHOTS.mkdir(parents=True, exist_ok=True)
    chan_slug = CHANNEL.lstrip("#")

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        ctx = browser.new_context(viewport={"width": 1100, "height": 720})
        page = ctx.new_page()

        # 1. Index page.
        print(f"[playwright] GET {VIEWER_URL}/", flush=True)
        page.goto(f"{VIEWER_URL}/")
        expect(page).to_have_title("agent-irc public viewer")
        link = page.locator(f'a.ch:has-text("{CHANNEL}")')
        expect(link).to_be_visible()
        page.screenshot(path=SCREENSHOT_INDEX, full_page=True)
        print(f"[playwright] ✓ index renders; screenshot → {SCREENSHOT_INDEX}", flush=True)

        # 2. Channel page + SSE connection.
        link.click()
        page.wait_for_url(f"**/c/{chan_slug}")
        page.wait_for_function(
            "document.getElementById('status').textContent.includes('live')",
            timeout=5000,
        )
        print("[playwright] ✓ channel page loaded, SSE connected", flush=True)

        # 3. Inject a live message via the CLI; the page should append it.
        marker = f"playwright-live-{int(time.time())}"
        run_cli("connect", "localhost:17000", "--nick", "pwprobe")
        try:
            run_cli("join", CHANNEL, "--nick", "pwprobe")
            time.sleep(0.4)
            run_cli("send", CHANNEL, "--nick", "pwprobe", marker)
            print(f"[playwright] sent via CLI: {marker}", flush=True)

            page.wait_for_selector(f'.text:has-text("{marker}")', timeout=5000)
            print(f"[playwright] ✓ live message {marker!r} rendered via SSE", flush=True)

            # 4. Screenshot the populated channel page.
            page.screenshot(path=SCREENSHOT_CHANNEL, full_page=True)
            print(f"[playwright] ✓ screenshot → {SCREENSHOT_CHANNEL}", flush=True)
        finally:
            try:
                run_cli("quit", "--nick", "pwprobe")
            except Exception:
                pass

        browser.close()

    print("PASS: viewer UI exercised end-to-end", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
