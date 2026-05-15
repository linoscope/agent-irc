#!/usr/bin/env bash
# Local Ethereum devnet — same shape as chapter 08b/10.
# Anvil's deterministic test mnemonic gives us the same accounts every run,
# so deploy.sh can register alice-bot/bob-bot from known keypairs without
# any "first run" bootstrap.
set -euo pipefail
exec anvil --port 8545 --silent --block-time 1
