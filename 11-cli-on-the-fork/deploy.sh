#!/usr/bin/env bash
# deploy.sh — chapter 11 (local): deploy a spec-compliant ERC-8004 Identity
# Registry to anvil, then register alice-bot / bob-bot / monitor on-chain.
#
# Unlike the canonical-on-Base flow (verify-mainnet.sh), this is fully
# offline: the agent JSON is inlined into the on-chain agentURI via a
# data: URI, so the fork can resolve names without any HTTP roundtrip.
#
# Writes:
#   .registry-address      → contract address (read by start-ergo.sh)
#   keys/<nick>.key        → wallet key (read by `agent-irc connect`)
#   keys/<nick>.agentid    → ERC-721 token id from the Registered event
set -euo pipefail
cd "$(dirname "$0")"
RPC="${RPC:-http://localhost:8545}"

DEPLOYER_KEY="0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
ALICE_KEY="0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
BOB_KEY="0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
MONITOR_KEY="0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6"

mkdir -p keys
echo "$ALICE_KEY"   > keys/alice-bot.key
echo "$BOB_KEY"     > keys/bob-bot.key
echo "$MONITOR_KEY" > keys/monitor.key
chmod 600 keys/*.key

forge build > /dev/null

DEPLOY_OUT=$(forge create --broadcast --rpc-url "$RPC" \
    --private-key "$DEPLOYER_KEY" \
    contracts/AgentRegistry.sol:AgentRegistry)
REGISTRY_ADDR=$(echo "$DEPLOY_OUT" | awk '/Deployed to:/ {print $3}')
echo "$REGISTRY_ADDR" > .registry-address
echo ">> registry @ $REGISTRY_ADDR"

# Topic-0 of Registered(uint256,string,address) — pinned so we don't shell out
# to `cast keccak` on every register.
REG_TOPIC0=0xca52e62c367d81bb2e328eb795f7c7ba24afb478408a26c0e201d155c449bc4a

# Register an agent with an inlined name JSON (data: URI). Capture the agentId
# from the Registered event's topic1.
register_agent() {
    local nick="$1" key="$2"
    local uri="data:application/json,{\"name\":\"$nick\"}"
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
    echo "$agent_id" > "keys/$nick.agentid"
    echo ">> $nick: agentId=$agent_id"
}

register_agent alice-bot "$ALICE_KEY"
register_agent bob-bot   "$BOB_KEY"
register_agent monitor   "$MONITOR_KEY"
