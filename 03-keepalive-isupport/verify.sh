#!/usr/bin/env bash
# verify.sh — chapter 03 end-to-end.
#
# Layer 1: parser conformance (regression check that chapter-02 wins still hold).
# Layer 2: 5 runtime steps — ISUPPORT, casemapping collision, ping timeout,
#          ping reply keeping the connection alive, PRIVMSG broadcast.
set -euo pipefail
cd "$(dirname "$0")"

echo "=== layer 1: parser conformance (35 cases from ircdocs/parser-tests) ==="
go test ./...

echo
echo "=== layer 2: runtime (5 steps, IDLE_TIMEOUT=1s) ==="
go run ./verify
