#!/usr/bin/env bash
# start-ergo.sh — chapter 06 launcher.
# Same as chapter 05 but on a different port and with a different data dir
# so chapter 05/06 verifies don't interfere if both were started.
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc}"
PORT="${PORT:-16672}"

echo ">> building agent-irc-ergo from $ERGO_SRC into $ERGO_BIN"
( cd "$ERGO_SRC" && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data

# Pull the chapter-04 minimal config and fix the listener.
if [[ ! -f ircd.yaml ]]; then
    cp ../05b-vendor-capability/ircd.yaml .
fi
sed -i -E "s/\":[0-9]+\":/\":$PORT\":/" ircd.yaml

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
