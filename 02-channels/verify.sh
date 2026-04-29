#!/usr/bin/env bash
# verify.sh — exits 0 iff chapter 02 works end-to-end.
#
# Two layers:
#   1. Parser conformance against the vendored ircdocs/parser-tests YAML.
#   2. Runtime: spin up the server, two clients exchange PRIVMSG via #room.
set -euo pipefail
cd "$(dirname "$0")"

echo "=== layer 1: parser conformance (35 cases from ircdocs/parser-tests) ==="
go test ./...

echo
echo "=== layer 2: runtime (alice/bob in #room) ==="
go run ./verify
