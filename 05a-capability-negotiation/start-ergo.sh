#!/usr/bin/env bash
# start-ergo.sh — chapter 05a uses *upstream* Ergo (~/workspace/ergo).
#
# This chapter is about understanding CAP at the protocol level. We don't
# need any fork modifications — every standard IRCv3 cap (account-tag,
# sasl, batch, message-tags, ...) is already there. Chapter 05b adds the
# vendor cap on top.
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/ergo}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo}"
PORT="${PORT:-16671}"

if [[ ! -x "$ERGO_BIN" ]]; then
    echo ">> building upstream Ergo from $ERGO_SRC into $ERGO_BIN"
    ( cd "$ERGO_SRC" && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )
fi

rm -rf data
mkdir -p data
sed -i -E "s/\":[0-9]+\":/\":$PORT\":/" ircd.yaml

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
