#!/usr/bin/env bash
# Build the agent-irc-ergo fork at the chapter-10 tag and start it with the
# ERC8004 registry config injected. The CLI side then authenticates with
# `agent-irc connect ... --erc8004-key keys/<nick>.key ...`.
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_TAG="${ERGO_TAG:-chapter-11}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc-ch11}"
PORT="${PORT:-16678}"
RPC="${RPC:-http://localhost:8545}"
REGISTRY_ADDR=$(cat .registry-address)

echo ">> checking out $ERGO_TAG in $ERGO_SRC and building into $ERGO_BIN"
( cd "$ERGO_SRC" \
  && git -c advice.detachedHead=false checkout "$ERGO_TAG" >/dev/null 2>&1 \
  && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data

# Regenerate ircd.yaml from defaultconfig so we always pick up the fork's
# current schema, then patch in the listener / paths / ERC8004 block.
"$ERGO_BIN" defaultconfig > ircd.yaml
python3 <<PY
import re
p = "ircd.yaml"
src = open(p).read()
src = re.sub(
    r'    listeners:.*?(?=\n    unix-bind-mode|\n    tor-listeners)',
    f'    listeners:\n        ":$PORT":\n',
    src, count=1, flags=re.DOTALL,
)
# Always-on lets nicks stay registered when their session drops — matches the
# appendix-cli-agent setup so the CLI's daemon-keeps-the-seat semantics hold.
src = src.replace("always-on: \"opt-in\"", "always-on: \"mandatory\"")
src = src.replace("    enabled: true\n\n    # default language", "    enabled: false\n\n    # default language")
src = src.replace("path: ircd.db", "path: data/ircd.db")
src = src.replace('lock-file: "ircd.lock"', 'lock-file: "data/ircd.lock"')
new_block = """
    erc8004:
        rpc-url: "$RPC"
        registry-address: "$REGISTRY_ADDR"
        chain-id: 31337
        cache-ttl: 0s
"""
src = re.sub(r'(\naccounts:\n)', r'\1' + new_block, src)
open(p, "w").write(src)
PY

"$ERGO_BIN" initdb --conf ircd.yaml --quiet
exec "$ERGO_BIN" run --conf ircd.yaml
