#!/usr/bin/env bash
# Same as chapter 08b but on a different port + cribs from chapter 08b's
# ircd.yaml as a starting point.
set -euo pipefail
cd "$(dirname "$0")"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_TAG="${ERGO_TAG:-chapter-09}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc-ch09}"
PORT="${PORT:-16675}"
RPC="${RPC:-http://localhost:8545}"
REGISTRY_ADDR=$(cat .registry-address 2>/dev/null || true)
if [[ -z "$REGISTRY_ADDR" ]]; then
    echo "ERROR: ./.registry-address missing — run ./deploy.sh first." >&2
    exit 1
fi

echo ">> checking out $ERGO_TAG in $ERGO_SRC and building into $ERGO_BIN"
( cd "$ERGO_SRC" \
  && git -c advice.detachedHead=false checkout "$ERGO_TAG" >/dev/null 2>&1 \
  && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data

# Regenerate ircd.yaml deterministically from defaults each run. Trying to
# patch a previously-patched file with regex is fragile; defaultconfig is
# the only stable starting point.
"$ERGO_BIN" defaultconfig > ircd.yaml
sed -i -E "s|    listeners:.*?(?=\n    unix-bind-mode|\n    tor-listeners)|    listeners:\n        \":$PORT\":\n|s" ircd.yaml || true
# The above multi-line sed is finicky; do it in python for reliability.
python3 <<PY
import re
p = "ircd.yaml"
src = open(p).read()
# Replace listeners with one plaintext listener.
src = re.sub(
    r'    listeners:.*?(?=\n    unix-bind-mode|\n    tor-listeners)',
    f'    listeners:\n        ":$PORT":\n',
    src, count=1, flags=re.DOTALL,
)
# Disable languages.
src = src.replace("    enabled: true\n\n    # default language", "    enabled: false\n\n    # default language")
# Pin lock + db paths into ./data.
src = src.replace("path: ircd.db", "path: data/ircd.db")
src = src.replace('lock-file: "ircd.lock"', 'lock-file: "data/ircd.lock"')
# Insert the erc8004 block as the first child under `accounts:`.
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
