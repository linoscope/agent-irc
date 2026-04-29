#!/usr/bin/env bash
# deploy.sh — chapter 09: deploys AgentRegistry, registers two agents.
#   account 1 → "alice-bot"          (valid IRC name)
#   account 2 → "bad name with spaces" (invalid IRC name; tests rejection)
set -euo pipefail
cd "$(dirname "$0")"

RPC="${RPC:-http://localhost:8545}"
DEPLOYER_KEY="0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

# anvil account 1
AGENT_KEY_1="0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
AGENT_ADDR_1="0x70997970C51812dc3A010C7d01b50e0d17dc79C8"
# anvil account 2 — the contract will accept any non-empty string, but
# Ergo's ValidateIRCName will reject names with spaces / non-ASCII.
AGENT_KEY_2="0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
AGENT_ADDR_2="0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"

forge build > /dev/null

DEPLOY_OUT=$(forge create --broadcast --rpc-url "$RPC" \
    --private-key "$DEPLOYER_KEY" \
    contracts/AgentRegistry.sol:AgentRegistry)
REGISTRY_ADDR=$(echo "$DEPLOY_OUT" | awk '/Deployed to:/ {print $3}')
echo "$REGISTRY_ADDR" > .registry-address
echo ">> registry deployed at $REGISTRY_ADDR"

echo ">> registering account 1 as 'alice-bot' (valid IRC name)"
cast send --rpc-url "$RPC" --private-key "$AGENT_KEY_1" \
    "$REGISTRY_ADDR" "register(string)" "alice-bot" > /dev/null

echo ">> registering account 2 as 'bad name' (invalid: contains space)"
cast send --rpc-url "$RPC" --private-key "$AGENT_KEY_2" \
    "$REGISTRY_ADDR" "register(string)" "bad name" > /dev/null

echo ">> sanity:"
echo "    nameOf($AGENT_ADDR_1) = $(cast call --rpc-url "$RPC" "$REGISTRY_ADDR" "nameOf(address) returns (string)" "$AGENT_ADDR_1")"
echo "    nameOf($AGENT_ADDR_2) = $(cast call --rpc-url "$RPC" "$REGISTRY_ADDR" "nameOf(address) returns (string)" "$AGENT_ADDR_2")"
