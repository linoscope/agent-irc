#!/usr/bin/env bash
# deploy.sh — chapter 09: deploy the canonical ERC-8004 Identity Registry,
# then register THREE test agents whose agent-JSON `.name` field exercises
# the three branches of the IRC-nick handler:
#
#   agent 1 — `alice-bot`                  (valid IRC name; SASL succeeds,
#                                           server forces NICK to alice-bot)
#   agent 2 — `bad name with spaces`       (invalid charset; SASL 904)
#   agent 3 — 40-char string > MaxIRCNameLen=32 (invalid length; SASL 904)
#
# Each registration passes the JSON inline via a `data:application/json,...`
# tokenURI so the fork's `Resolve` path can fetch the `.name` without any
# HTTP round-trip. The on-chain registry treats the URI string as opaque;
# the JSON-parsing and ValidateIRCName checks happen entirely in the fork's
# SASL handler.
#
# We capture each minted agentId from the `Registered(uint256,string,address)`
# event's indexed topic1 and write it to .alice-agentid / .bad-agentid /
# .long-agentid so verify/main.go can pick them up by name.
set -euo pipefail
cd "$(dirname "$0")"

RPC="${RPC:-http://localhost:8545}"

# anvil's deterministic mnemonic — account 0 is the deployer, 1/2/3 are the
# three test agents. Keep the agent keys in sync with verify/main.go.
DEPLOYER_KEY="0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
ALICE_KEY="0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
BAD_KEY="0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
LONG_KEY="0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6"

forge build > /dev/null

DEPLOY_OUT=$(forge create --broadcast --rpc-url "$RPC" \
    --private-key "$DEPLOYER_KEY" \
    contracts/AgentRegistry.sol:AgentRegistry)
REGISTRY_ADDR=$(echo "$DEPLOY_OUT" | awk '/Deployed to:/ {print $3}')
echo "$REGISTRY_ADDR" > .registry-address
echo ">> registry @ $REGISTRY_ADDR"

# Topic-0 of Registered(uint256,string,address). Pinned so we don't shell
# out to `cast keccak` on every register.
REG_TOPIC0=0xca52e62c367d81bb2e328eb795f7c7ba24afb478408a26c0e201d155c449bc4a

# register_agent <out-file> <name> <agent-key>
#
# Submits a register(string) tx with a data: URI inlining {"name":"<name>"}
# as the agentURI. Parses the receipt logs for Registered, grabs the agentId
# (the indexed topic1), and writes it to <out-file> in decimal.
register_agent() {
    local out_file="$1" name="$2" key="$3"
    local uri="data:application/json,{\"name\":\"$name\"}"
    local tx
    tx=$(cast send --rpc-url "$RPC" --private-key "$key" --json \
            "$REGISTRY_ADDR" "register(string)" "$uri" \
            | python3 -c "import sys,json; print(json.load(sys.stdin)['transactionHash'])")
    local agent_id
    agent_id=$(cast receipt --rpc-url "$RPC" "$tx" --json | python3 -c "
import sys, json
r = json.load(sys.stdin)
for log in r.get('logs', []):
    if log['topics'][0] == '$REG_TOPIC0':
        print(int(log['topics'][1], 16))
        break
")
    echo "$agent_id" > "$out_file"
    echo ">> $out_file: agentId=$agent_id  name=$(printf %q "$name")"
}

# A 40-character ASCII string — well past MaxIRCNameLen=32 — so the
# validator rejects on length rather than charset.
LONG_NAME="alice-bot-with-a-name-far-too-long-to-fit"

register_agent .alice-agentid "alice-bot"             "$ALICE_KEY"
register_agent .bad-agentid   "bad name with spaces"  "$BAD_KEY"
register_agent .long-agentid  "$LONG_NAME"            "$LONG_KEY"

echo ">> sanity (tokenURI per agentId):"
for f in .alice-agentid .bad-agentid .long-agentid; do
    id=$(cat "$f")
    uri=$(cast call --rpc-url "$RPC" "$REGISTRY_ADDR" "tokenURI(uint256) (string)" "$id" | sed 's/^"//; s/"$//')
    echo "    tokenURI($id) = $uri"
done
