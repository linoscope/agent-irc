#!/usr/bin/env bash
# start-ergo.sh — build Ergo (if needed), reset its data dir, and run it.
#
# Idempotent: rerunning is safe. Ergo state lives in ./data, which is removed
# at the start of each run so the chapter is reproducible.
#
# Env:
#   ERGO_SRC   path to Ergo source clone (default: ~/workspace/ergo)
#   ERGO_BIN   path to Ergo binary (default: /tmp/ergo)
#   PORT       chapter listener port (default: 16670; rewritten in ircd.yaml)
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/ergo}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo}"
PORT="${PORT:-16670}"

if [[ ! -x "$ERGO_BIN" ]]; then
    echo ">> building Ergo from $ERGO_SRC into $ERGO_BIN"
    ( cd "$ERGO_SRC" && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )
fi

# Reset state.
rm -rf data
mkdir -p data

# Patch the listener if PORT differs from the file's value.
sed -i -E "s/\":[0-9]+\":/\":$PORT\":/" ircd.yaml

# Initialize DB and run.
"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
