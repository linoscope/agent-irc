#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc}"
PORT="${PORT:-16676}"
RPC="${RPC:-http://localhost:8545}"
REGISTRY_ADDR=$(cat .registry-address)

( cd "$ERGO_SRC" && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data

# Regenerate ircd.yaml deterministically from defaultconfig.
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
# Watcher polls every 1s for the test (default is 30s).
exec env AGENT_IRC_WATCHER_INTERVAL=1 "$ERGO_BIN" run --conf ircd.yaml
