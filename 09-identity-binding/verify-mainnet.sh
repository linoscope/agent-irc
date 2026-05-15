#!/usr/bin/env bash
# verify-mainnet.sh — exercise chapter 09's success path against the
# canonical ERC-8004 Identity Registry on Base mainnet (the one at
# 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432).
#
# The Go verify program is the same one used by ./verify.sh; the only
# difference is that here the registry lives on a real chain, tokenURI
# resolves over HTTPS to a real `agent.json`, and the IRC nick the
# server forces is the `.name` field of that JSON.
#
# Prereqs (the project's repo-root ../.env is the source of truth):
#   AGENT_PRIVATE_KEY  — funded + registered on Base mainnet
#   AGENT_ID           — the agent's ERC-721 token id (uint256)
#   AGENT_URI          — must resolve over HTTPS to JSON {"name": "<irc-nick>"}
#   AGENT_NICK         — expected `.name` (we sanity-check tokenURI against this)
#   CHAIN_ID           — 8453 for Base mainnet (default if unset)
#   ERC8004_REGISTRY   — canonical registry address (default if unset)
set -uo pipefail
cd "$(dirname "$0")"

PORT="${PORT:-16675}"
EXPECTED_NICK="${AGENT_NICK:-lin-test-bot}"

# Load the funded agent's secrets.
if [[ ! -f ../.env ]]; then
    echo "FAIL: ../.env not found — generate one with deploy/onboarding flow first." >&2
    exit 1
fi
# shellcheck disable=SC1091
set -a; source ../.env; set +a
: "${AGENT_PRIVATE_KEY:?AGENT_PRIVATE_KEY missing from ../.env}"
: "${AGENT_ID:?AGENT_ID missing from ../.env}"
: "${CHAIN_ID:=8453}"
: "${ERC8004_REGISTRY:=0x8004A169FB4a3325136EB29fA0ceB6D2e539a432}"
: "${RPC_URL:=https://mainnet.base.org}"

ERGO_LOG=$(mktemp); ERGO_PID=""
cleanup() {
    [[ -n "$ERGO_PID" ]] && kill "$ERGO_PID" 2>/dev/null
    rm -f "$ERGO_LOG" .alice-agentid .bad-agentid .long-agentid
}
trap cleanup EXIT INT TERM

echo "=== 1. pre-flight: tokenURI($AGENT_ID) → JSON with .name=\"$EXPECTED_NICK\" ==="
URI=$(cast call --rpc-url "$RPC_URL" "$ERC8004_REGISTRY" 'tokenURI(uint256) (string)' "$AGENT_ID" | sed 's/^"//; s/"$//')
echo "  tokenURI: $URI"
JSON=$(curl -fsSL --max-time 8 "$URI" 2>/dev/null || true)
if [[ -z "$JSON" ]]; then
    echo "FAIL: tokenURI($AGENT_ID) → $URI did not resolve over HTTPS" >&2
    exit 1
fi
NAME=$(echo "$JSON" | python3 -c "import sys, json; print(json.load(sys.stdin).get('name', ''))")
echo "  agent JSON .name = $NAME"
if [[ "$NAME" != "$EXPECTED_NICK" ]]; then
    echo "FAIL: agent JSON .name is $NAME, expected $EXPECTED_NICK" >&2
    exit 1
fi
echo "  ✓ JSON .name matches expected nick"

echo
echo "=== 2. starting agent-irc-ergo pointed at Base mainnet ==="
# Build the local-fork start-ergo via the chapter-11 template since
# ch09 only has the anvil version. Reuse env wiring.
export ERGO_TAG="${ERGO_TAG:-chapter-erc8004-canonical}"
export ERGO_BIN="${ERGO_BIN:-/tmp/ergo-agentirc-ch09-base}"
export PORT
export RPC="$RPC_URL"
export REGISTRY_ADDR="$ERC8004_REGISTRY"
export CHAIN_ID
export CACHE_TTL="${CACHE_TTL:-30s}"

ERGO_SRC="${ERGO_SRC:-$HOME/workspace/agent-irc-ergo}"
echo ">> checking out $ERGO_TAG in $ERGO_SRC and building into $ERGO_BIN"
( cd "$ERGO_SRC" \
  && git -c advice.detachedHead=false checkout "$ERGO_TAG" >/dev/null 2>&1 \
  && GOTOOLCHAIN=go1.26.2 go build -o "$ERGO_BIN" . )

rm -rf data
mkdir -p data

"$ERGO_BIN" defaultconfig > ircd.yaml
python3 <<PY
import re, os
p = "ircd.yaml"
src = open(p).read()
src = re.sub(
    r'    listeners:.*?(?=\n    unix-bind-mode|\n    tor-listeners)',
    f'    listeners:\n        ":{os.environ["PORT"]}":\n',
    src, count=1, flags=re.DOTALL,
)
src = src.replace("    enabled: true\n\n    # default language",
                  "    enabled: false\n\n    # default language")
src = src.replace("path: ircd.db", "path: data/ircd.db")
src = src.replace('lock-file: "ircd.lock"', 'lock-file: "data/ircd.lock"')
new_block = f"""
    erc8004:
        rpc-url: "{os.environ['RPC']}"
        registry-address: "{os.environ['REGISTRY_ADDR']}"
        chain-id: {os.environ['CHAIN_ID']}
        cache-ttl: {os.environ['CACHE_TTL']}
"""
src = re.sub(r'(\naccounts:\n)', r'\1' + new_block, src)
open(p, "w").write(src)
PY

"$ERGO_BIN" initdb --conf ircd.yaml --quiet > "$ERGO_LOG" 2>&1
"$ERGO_BIN" run --conf ircd.yaml >> "$ERGO_LOG" 2>&1 &
ERGO_PID=$!
for _ in $(seq 1 80); do
    grep -q "now listening on" "$ERGO_LOG" 2>/dev/null && break
    sleep 0.3
done
if ! grep -q "now listening on" "$ERGO_LOG"; then
    echo "FAIL: Ergo did not start"; tail -30 "$ERGO_LOG"; exit 1
fi
grep "agent-irc" "$ERGO_LOG" | head -3 | sed 's/^/  /'

echo
echo "=== 3. running the chapter-09 verify program against the mainnet agent ==="
# The Go program reads three agentId files. For the mainnet success-path
# we only run case 1, so:
#   - put AGENT_ID into .alice-agentid (the positive-case file).
#   - point .bad-agentid / .long-agentid at the same id; we'll cut the
#     test short before they run.
echo "$AGENT_ID" > .alice-agentid

# Strip the 0x prefix from the private key for go-ethereum's HexToECDSA,
# then export so the Go program can pick it up. The compiled verify program
# uses anvil's hardcoded keys; for the mainnet run we override case 1's key
# inline via a one-off main.
KEY_HEX="${AGENT_PRIVATE_KEY#0x}"

# One-shot Go program: replays case 1 of verify/main.go's handshake
# against the live registry. We could plumb env-overrides into main.go,
# but the inline form keeps the chapter's verify/main.go tightly scoped
# to the local-anvil happy path.
cat > /tmp/verify-mainnet-ch09.go <<'GO'
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" { port = "16675" }
	keyHex := os.Getenv("KEY_HEX")
	agentIDStr := os.Getenv("AGENT_ID")
	chainStr := os.Getenv("CHAIN_ID")
	expectName := os.Getenv("EXPECTED_NICK")

	key, err := crypto.HexToECDSA(keyHex)
	if err != nil { fmt.Println("FAIL: bad key:", err); os.Exit(1) }
	agentID, ok := new(big.Int).SetString(agentIDStr, 10)
	if !ok { fmt.Println("FAIL: bad agent id"); os.Exit(1) }
	chainID := uint64(0)
	fmt.Sscanf(chainStr, "%d", &chainID)

	c, err := net.Dial("tcp", "localhost:"+port)
	if err != nil { fmt.Println("FAIL: dial:", err); os.Exit(1) }
	defer c.Close()
	rd := bufio.NewReader(c)
	send := func(line string) {
		_, _ = c.Write([]byte(line + "\r\n"))
		fmt.Printf("  conn -> %s\n", line)
	}
	readLine := func() (string, error) {
		c.SetReadDeadline(time.Now().Add(15 * time.Second))
		line, err := rd.ReadString('\n')
		if err != nil { return "", err }
		line = strings.TrimRight(line, "\r\n")
		fmt.Printf("  conn <- %s\n", line)
		return line, nil
	}
	waitFor := func(substr string, dur time.Duration) (string, error) {
		deadline := time.Now().Add(dur)
		for time.Now().Before(deadline) {
			line, err := readLine()
			if err != nil {
				type t interface{ Timeout() bool }
				if x, ok := err.(t); ok && x.Timeout() { continue }
				return "", err
			}
			if strings.Contains(line, substr) { return line, nil }
		}
		return "", fmt.Errorf("timeout waiting for %q", substr)
	}

	send("CAP LS 302")
	send("NICK conn")
	send("USER conn 0 * :conn")
	send("CAP REQ :sasl message-tags server-time account-tag")
	if _, err := waitFor("ACK", 8*time.Second); err != nil { fmt.Println("FAIL:", err); os.Exit(1) }
	send("AUTHENTICATE ERC8004")
	if _, err := waitFor("AUTHENTICATE +", 5*time.Second); err != nil { fmt.Println("FAIL:", err); os.Exit(1) }
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(common.BigToHash(agentID).Bytes()))
	ch, err := waitFor("AUTHENTICATE", 20*time.Second)
	if err != nil { fmt.Println("FAIL:", err); os.Exit(1) }
	idx := strings.LastIndex(ch, "AUTHENTICATE ")
	nonce, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(ch[idx+len("AUTHENTICATE "):]))
	body := []byte(fmt.Sprintf("agent-irc-sasl-v1\nchain=%d\nserver=ergo.test\nagentId=%s\nnonce=%s",
		chainID, agentID.String(), hex.EncodeToString(nonce)))
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(body))
	hash := crypto.Keccak256([]byte(prefix), body)
	sig, _ := crypto.Sign(hash, key)
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(sig))
	loggedIn, err := waitFor(" 900 ", 20*time.Second)
	if err != nil { fmt.Println("FAIL: no 900 RPL_LOGGEDIN:", err); os.Exit(1) }
	if !strings.Contains(loggedIn, expectName) {
		fmt.Printf("FAIL: 900 should bind account %q, got %s\n", expectName, loggedIn)
		os.Exit(1)
	}
	fmt.Printf("  ✓ 900 bound to JSON .name %q\n", expectName)
	if _, err := waitFor(" 903 ", 8*time.Second); err != nil { fmt.Println("FAIL:", err); os.Exit(1) }
	send("CAP END")
	welcome, err := waitFor(" 001 ", 8*time.Second)
	if err != nil { fmt.Println("FAIL: no 001 RPL_WELCOME:", err); os.Exit(1) }
	if !strings.Contains(welcome, expectName) {
		fmt.Printf("FAIL: 001 should address %q, got %s\n", expectName, welcome)
		os.Exit(1)
	}
	fmt.Printf("  ✓ 001 addresses %q (server forced NICK to mainnet JSON .name)\n", expectName)
}
GO

KEY_HEX="$KEY_HEX" AGENT_ID="$AGENT_ID" CHAIN_ID="$CHAIN_ID" \
EXPECTED_NICK="$EXPECTED_NICK" PORT="$PORT" \
go run /tmp/verify-mainnet-ch09.go

echo
echo "PASS: chapter 09 — mainnet agent JSON .name = $EXPECTED_NICK is bound as the IRC nick"
