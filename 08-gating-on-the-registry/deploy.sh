#!/usr/bin/env bash
# deploy.sh — deploys AgentRegistry to anvil and registers one test agent.
#
# Outputs the registry address to ./.registry-address so other scripts can
# read it. Uses anvil's well-known default accounts (DO NOT use these on
# any real chain — the private keys are public).
set -euo pipefail
cd "$(dirname "$0")"

RPC="${RPC:-http://localhost:8545}"
# anvil account 0 (deployer)
DEPLOYER_KEY="0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
# anvil account 1 (will register as the test agent)
AGENT_KEY="0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
AGENT_ADDR="0x70997970C51812dc3A010C7d01b50e0d17dc79C8"

forge build > /dev/null

# Compile output puts the contract at out/AgentRegistry.sol/AgentRegistry.json.
echo ">> deploying AgentRegistry"
DEPLOY_OUT=$(forge create --broadcast --rpc-url "$RPC" \
    --private-key "$DEPLOYER_KEY" \
    contracts/AgentRegistry.sol:AgentRegistry)
REGISTRY_ADDR=$(echo "$DEPLOY_OUT" | awk '/Deployed to:/ {print $3}')
if [[ -z "$REGISTRY_ADDR" ]]; then
    echo "ERROR: could not parse deployment address"
    echo "$DEPLOY_OUT"
    exit 1
fi
echo "$REGISTRY_ADDR" > .registry-address
echo ">> registry deployed at $REGISTRY_ADDR"

echo ">> registering anvil-account-1 as agent 'alice-bot'"
cast send --rpc-url "$RPC" --private-key "$AGENT_KEY" \
    "$REGISTRY_ADDR" "register(string)" "alice-bot" > /dev/null
echo ">> done. agent address = $AGENT_ADDR"

# Sanity check: registry returns the right name.
NAME=$(cast call --rpc-url "$RPC" "$REGISTRY_ADDR" \
    "nameOf(address) returns (string)" "$AGENT_ADDR")
echo ">> sanity check nameOf($AGENT_ADDR) = $NAME"
