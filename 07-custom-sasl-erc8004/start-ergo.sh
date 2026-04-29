#!/usr/bin/env bash
# start-ergo.sh — chapter 07 launcher.
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_TAG="${ERGO_TAG:-chapter-07}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc-ch07}"
PORT="${PORT:-16673}"

echo ">> checking out $ERGO_TAG in $ERGO_SRC and building into $ERGO_BIN"
( cd "$ERGO_SRC" \
  && git -c advice.detachedHead=false checkout "$ERGO_TAG" >/dev/null 2>&1 \
  && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data

if [[ ! -f ircd.yaml ]]; then
    cp ../06-sasl-and-account-tag/ircd.yaml .
fi
sed -i -E "s/\":[0-9]+\":/\":$PORT\":/" ircd.yaml

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
