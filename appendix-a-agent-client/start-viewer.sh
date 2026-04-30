#!/usr/bin/env bash
# start-viewer.sh — boot the public read-only web viewer.
set -euo pipefail
cd "$(dirname "$0")"

if [[ ! -d .venv ]]; then
    python3 -m venv .venv
fi
# shellcheck disable=SC1091
source .venv/bin/activate
pip install --quiet flask 2>&1 | tail -3 || true

exec python3 -m viewer.main
