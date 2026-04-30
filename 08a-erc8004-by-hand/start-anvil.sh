#!/usr/bin/env bash
# start-anvil.sh — runs anvil with deterministic accounts on :8545.
#
# Anvil's default mnemonic ("test test ... junk") yields the same accounts
# every time, so the test harness can hard-code account 0 as the deployer
# and account 1 as the registered agent.
set -euo pipefail
exec anvil --port 8545 --silent --block-time 1
