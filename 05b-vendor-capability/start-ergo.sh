#!/usr/bin/env bash
# start-ergo.sh — pin the agent-irc fork to chapter 05's tag, build, run.
#
# Each chapter pins the fork to its own tag so cross-chapter changes don't
# bleed in. See AGENTS.md "Repository layout" for the tag scheme.
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_TAG="${ERGO_TAG:-chapter-05}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc-ch05}"
PORT="${PORT:-16677}"

echo ">> checking out $ERGO_TAG in $ERGO_SRC and building into $ERGO_BIN"
( cd "$ERGO_SRC" \
  && git -c advice.detachedHead=false checkout "$ERGO_TAG" >/dev/null 2>&1 \
  && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data
sed -i -E "s/\":[0-9]+\":/\":$PORT\":/" ircd.yaml

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
