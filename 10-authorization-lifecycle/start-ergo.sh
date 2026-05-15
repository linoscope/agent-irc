#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
ERGO_TAG="${ERGO_TAG:-chapter-erc8004-canonical}"
ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc-ch10}"
PORT="${PORT:-16676}"
RPC="${RPC:-http://localhost:8545}"
REGISTRY_ADDR=$(cat .registry-address)

echo ">> checking out $ERGO_TAG in $ERGO_SRC and building into $ERGO_BIN"
( cd "$ERGO_SRC" \
  && git -c advice.detachedHead=false checkout "$ERGO_TAG" >/dev/null 2>&1 \
  && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

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
# Verbose logging so chapter readers can see PRIVMSG / userinput on stderr.
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
# Watcher polls every 1s for the test (default is 30s).
exec env AGENT_IRC_WATCHER_INTERVAL=1 "$ERGO_BIN" run --conf ircd.yaml
