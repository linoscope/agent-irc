#!/usr/bin/env bash
# start-ergo.sh — chapter 08 launcher with ERC-8004 gate enabled.
#
# Reads the registry address from ./.registry-address (written by deploy.sh).
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_TAG="${ERGO_TAG:-chapter-erc8004-canonical}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc-ch08}"
PORT="${PORT:-16674}"
RPC="${RPC:-http://localhost:8545}"
REGISTRY_ADDR=$(cat .registry-address 2>/dev/null || true)
if [[ -z "$REGISTRY_ADDR" ]]; then
    echo "ERROR: ./.registry-address missing — run ./deploy.sh first."
    exit 1
fi

echo ">> checking out $ERGO_TAG in $ERGO_SRC and building into $ERGO_BIN"
( cd "$ERGO_SRC" \
  && git -c advice.detachedHead=false checkout "$ERGO_TAG" >/dev/null 2>&1 \
  && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data

# Regenerate ircd.yaml deterministically from defaultconfig each run, same
# pattern as ch09/ch10. The earlier "patch in place" approach had a regex
# bug where re-runs deleted sibling sub-keys under accounts:.
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
src = src.replace("    enabled: true\n\n    # default language", "    enabled: false\n\n    # default language")
src = src.replace("path: ircd.db", "path: data/ircd.db")
src = src.replace('lock-file: "ircd.lock"', 'lock-file: "data/ircd.lock"')
# Verbose logging for the tutorial.
src = src.replace('type: "* -userinput -useroutput"', 'type: "* userinput useroutput"')
src = re.sub(r'^        level: info$', '        level: debug', src, count=1, flags=re.MULTILINE)
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
