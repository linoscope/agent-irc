#!/usr/bin/env bash
# start-ergo.sh — boot upstream Ergo for the appendix-cli-agent demo.
#
# This appendix runs against stock Ergo (no fork). Listens on :17000 plaintext.
# always-on is "mandatory" so registered accounts persist in joined channels
# even when their socket disconnects — the substrate every CLI invocation
# relies on.
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/ergo}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo}"
PORT="${PORT:-17000}"

if [[ ! -x "$ERGO_BIN" ]]; then
    echo ">> building upstream Ergo from $ERGO_SRC into $ERGO_BIN"
    ( cd "$ERGO_SRC" && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )
fi

rm -rf data
mkdir -p data
# Defensive: clean stray lock files at the appendix root from earlier runs
# that used a different datastore.path.
rm -f ircd.lock ircd.db
sed -i -E "s/\":[0-9]+\":/\":$PORT\":/" ircd.yaml

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
