#!/usr/bin/env bash
# start-ergo.sh — appendix A launcher.
# Uses the upstream-ergo build (no agent-irc fork features needed for the appendix).
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/ergo}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-appendix-a}"
PORT="${PORT:-17000}"

echo ">> building upstream Ergo from $ERGO_SRC into $ERGO_BIN"
( cd "$ERGO_SRC" && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data
sed -i -E "s/\":[0-9]+\":/\":$PORT\":/" ircd.yaml

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
