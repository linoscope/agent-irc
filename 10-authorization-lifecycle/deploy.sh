#!/usr/bin/env bash
# deploy.sh — chapter 10: deploy the spec-compliant ERC-8004 Identity
# Registry to anvil, then register a single agent (alice-bot) we'll use
# to exercise the SASL flow and the on-chain mutation watcher.
#
# Unlike a real Base mainnet deployment, we inline the agent JSON via a
# data: URI so the fork can resolve `.name` without any HTTP roundtrip.
# Chapter 10's case-3 test then mutates that URI on-chain (setAgentURI)
# to change `.name` — the watcher detects the rename within a few seconds
# and KILLs alice-bot's session.
#
# Writes:
#   .registry-address    → contract address (read by start-ergo.sh + verify)
#   .alice-agentid       → ERC-721 token id from the Registered event
set -euo pipefail
cd "$(dirname "$0")"
RPC="${RPC:-http://localhost:8545}"

DEPLOYER_KEY="0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
# Anvil account #1 — used as alice-bot's wallet. Public knowledge; do not
# reuse for anything that holds value.
ALICE_KEY="0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"

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

URI='data:application/json,{"name":"alice-bot"}'
TX=$(cast send --rpc-url "$RPC" --private-key "$ALICE_KEY" --json \
        "$REGISTRY_ADDR" "register(string)" "$URI" \
        | python3 -c "import sys,json; print(json.load(sys.stdin)['transactionHash'])")
AGENT_ID=$(cast receipt --rpc-url "$RPC" "$TX" --json | python3 -c "
import sys, json
r = json.load(sys.stdin)
for log in r.get('logs', []):
    if log['topics'][0] == '$REG_TOPIC0':
        print(int(log['topics'][1], 16))
        break
")
echo "$AGENT_ID" > .alice-agentid
echo ">> registered alice-bot: agentId=$AGENT_ID  uri=$URI"
