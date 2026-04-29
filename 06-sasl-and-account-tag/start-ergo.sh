#!/usr/bin/env bash
# start-ergo.sh — chapter 06 launcher.
# Same as chapter 05 but on a different port and with a different data dir
# so chapter 05/06 verifies don't interfere if both were started.
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
# Chapter 06 introduces no fork changes; pin to chapter-05 (the latest fork
# state ch06's recipes were written against — it includes the vendor cap).
ERGO_TAG="${ERGO_TAG:-chapter-05}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc-ch05}"
PORT="${PORT:-16672}"

echo ">> checking out $ERGO_TAG in $ERGO_SRC and building into $ERGO_BIN"
( cd "$ERGO_SRC" \
  && git -c advice.detachedHead=false checkout "$ERGO_TAG" >/dev/null 2>&1 \
  && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data

# Pull the chapter-04 minimal config and fix the listener.
if [[ ! -f ircd.yaml ]]; then
    cp ../05b-vendor-capability/ircd.yaml .
fi
sed -i -E "s/\":[0-9]+\":/\":$PORT\":/" ircd.yaml

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
