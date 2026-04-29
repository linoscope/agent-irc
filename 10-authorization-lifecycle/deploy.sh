#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
RPC="${RPC:-http://localhost:8545}"
DEPLOYER_KEY="0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
AGENT_KEY="0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"

forge build > /dev/null

DEPLOY_OUT=$(forge create --broadcast --rpc-url "$RPC" \
    --private-key "$DEPLOYER_KEY" \
    contracts/AgentRegistry.sol:AgentRegistry)
REGISTRY_ADDR=$(echo "$DEPLOY_OUT" | awk '/Deployed to:/ {print $3}')
echo "$REGISTRY_ADDR" > .registry-address
echo ">> registry @ $REGISTRY_ADDR"

cast send --rpc-url "$RPC" --private-key "$AGENT_KEY" \
    "$REGISTRY_ADDR" "register(string)" "alice-bot" > /dev/null
echo ">> registered anvil-1 as alice-bot"
