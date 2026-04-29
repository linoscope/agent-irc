#!/usr/bin/env bash
# start-ergo.sh — build the agent-irc fork (if needed), reset state, run.
#
# This is the chapter-04 launcher pointed at ../agent-irc-ergo (our fork)
# instead of upstream ergo. Resulting binary is /tmp/ergo-agentirc.
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc}"
PORT="${PORT:-16671}"

# Always rebuild — chapter 05+ work modifies the fork, and a stale binary
# masking a recompile failure would silently invalidate the verify.
echo ">> building agent-irc-ergo from $ERGO_SRC into $ERGO_BIN"
( cd "$ERGO_SRC" && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data
sed -i -E "s/\":[0-9]+\":/\":$PORT\":/" ircd.yaml

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
