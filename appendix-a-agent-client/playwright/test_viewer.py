"""
Playwright UI test for the public viewer.

Exercises:
  1. Index page renders and lists the configured channel(s).
  2. Channel detail page renders previously-seen messages.
  3. SSE delivers a fresh live message to a page that's already loaded.
  4. Capture a screenshot of the loaded channel page for the README.

Run after `start-ergo.sh`, `start-viewer.sh`, and at least one agent
have produced some traffic in #agents-room.
"""
from __future__ import annotations

import os
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from agent_irc import IRCAgent  # noqa: E402
from playwright.sync_api import sync_playwright, expect  # noqa: E402


VIEWER_URL = os.environ.get("VIEWER_URL", "http://localhost:8080")
SCREENSHOTS = Path(__file__).resolve().parent.parent / "screenshots"
SCREENSHOT_INDEX = str(SCREENSHOTS / "viewer-index.png")
SCREENSHOT_CHANNEL = str(SCREENSHOTS / "viewer-channel.png")


def main() -> int:
    SCREENSHOTS.mkdir(parents=True, exist_ok=True)

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        ctx = browser.new_context(viewport={"width": 1100, "height": 720})
        page = ctx.new_page()

        # 1. Index page.
        print(f"[playwright] GET {VIEWER_URL}/", flush=True)
        page.goto(f"{VIEWER_URL}/")
        expect(page).to_have_title("agent-irc public viewer")
        link = page.locator('a.ch:has-text("#agents-room")')
        expect(link).to_be_visible()
        page.screenshot(path=SCREENSHOT_INDEX, full_page=True)
        print(f"[playwright] ✓ index renders; screenshot → {SCREENSHOT_INDEX}", flush=True)

        # 2. Channel page.
        link.click()
        page.wait_for_url("**/c/agents-room")
        # Wait for the live status indicator to flip to "● live"
        # (this confirms the SSE stream connected).
        page.wait_for_function(
            "document.getElementById('status').textContent.includes('live')",
            timeout=5000,
        )
        print("[playwright] ✓ channel page loaded, SSE connected", flush=True)

        # 3. Inject a live message via IRC; the page should append it.
        marker = f"playwright-live-{int(time.time())}"
        print(f"[playwright] sending live message to IRC: {marker}", flush=True)
        with IRCAgent("localhost", 17000, nick="pwprobe") as a:
            a.join("#agents-room")
            time.sleep(0.4)
            a.send_message("#agents-room", marker)
            time.sleep(0.6)

        # The page's SSE handler should have appended it.
        page.wait_for_selector(f'.text:has-text("{marker}")', timeout=5000)
        print(f"[playwright] ✓ live message {marker!r} appeared via SSE", flush=True)

        # 4. Screenshot the populated channel page.
        page.screenshot(path=SCREENSHOT_CHANNEL, full_page=True)
        print(f"[playwright] ✓ screenshot saved to {SCREENSHOT_CHANNEL}", flush=True)

        browser.close()

    print("PASS: viewer UI exercised end-to-end", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
