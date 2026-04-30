#!/usr/bin/env bash
# deploy.sh — chapter 08a: deploy the AgentRegistry to local anvil.
#
# Unlike chapter 08b's deploy.sh, this one does NOT auto-register a test
# agent. The chapter walks you through registering / querying / mutating
# entries by hand with `cast` so you build muscle memory.
#
# Writes the deployed registry address to ./.registry-address so the
# chapter recipe can refer to it as $(cat .registry-address).
set -euo pipefail
cd "$(dirname "$0")"

RPC="${RPC:-http://localhost:8545}"
# anvil's well-known account 0 — DO NOT use this on any real chain.
DEPLOYER_KEY="0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

forge build > /dev/null

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
echo ">> AgentRegistry deployed at $REGISTRY_ADDR"
echo ">> address saved to ./.registry-address"
echo ">> next: walk the recipe in README.md (cast call / cast send)"
