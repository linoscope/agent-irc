#!/usr/bin/env bash
# deploy.sh — chapter 08b: deploy the canonical ERC-8004 Identity Registry
# to anvil, then register ONE test agent (alice-bot) on-chain.
#
# Wire shape this chapter's SASL handler expects:
#   - client claims a uint256 agentId (NOT an address — the canonical
#     registry has no reverse address→agentId lookup);
#   - server resolves agentId → wallet via getAgentWallet(uint256).
#
# To make that resolve succeed we (a) deploy the registry, (b) call
# register(string) with a data: URI inlining the agent's .name JSON, and
# (c) capture the agentId from the Registered(uint256,string,address) log.
#
# Writes:
#   .registry-address   → contract address (read by start-ergo.sh)
#   .alice-agentid      → ERC-721 token id alice-bot was minted at
set -euo pipefail
cd "$(dirname "$0")"

RPC="${RPC:-http://localhost:8545}"
# anvil account 0 (deployer)
DEPLOYER_KEY="0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
# anvil account 1 (will register as alice-bot)
ALICE_KEY="0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"

forge build > /dev/null

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
echo ">> registry @ $REGISTRY_ADDR"

# Topic-0 of Registered(uint256,string,address) — pinned so we don't shell
# out to `cast keccak` on every register.
REG_TOPIC0=0xca52e62c367d81bb2e328eb795f7c7ba24afb478408a26c0e201d155c449bc4a

# Register alice-bot with an inlined data: URI so the fork can resolve the
# off-chain name without any HTTP roundtrip.
NICK="alice-bot"
URI="data:application/json,{\"name\":\"$NICK\"}"
echo ">> registering anvil-account-1 as agent '$NICK' (uri=$URI)"

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
if [[ -z "$AGENT_ID" ]]; then
    echo "ERROR: could not find Registered() log in receipt for $TX"
    exit 1
fi
echo "$AGENT_ID" > .alice-agentid
echo ">> $NICK: agentId=$AGENT_ID"

# Sanity check: getAgentWallet(agentId) must return alice's address, and
# tokenURI(agentId) must return the data: URI we just embedded.
ALICE_ADDR=$(cast wallet address --private-key "$ALICE_KEY")
WALLET=$(cast call --rpc-url "$RPC" "$REGISTRY_ADDR" \
    "getAgentWallet(uint256) (address)" "$AGENT_ID")
TOKEN_URI=$(cast call --rpc-url "$RPC" "$REGISTRY_ADDR" \
    "tokenURI(uint256) (string)" "$AGENT_ID" | sed 's/^"//; s/"$//')
echo ">> sanity check getAgentWallet($AGENT_ID) = $WALLET"
echo ">> sanity check tokenURI($AGENT_ID)      = $TOKEN_URI"
if [[ "${WALLET,,}" != "${ALICE_ADDR,,}" ]]; then
    echo "ERROR: getAgentWallet($AGENT_ID) = $WALLET != alice $ALICE_ADDR"
    exit 1
fi
