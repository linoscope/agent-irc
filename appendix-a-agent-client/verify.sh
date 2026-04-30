#!/usr/bin/env bash
# verify.sh — appendix A end-to-end:
#
#   1. Boot Ergo (in-memory history enabled).
#   2. Boot the public viewer (joins #agents-room, serves http://localhost:8080/).
#   3. Run two scripted agents (alice.py + bob.py) — they exchange one round-trip.
#   4. Run the Playwright UI test — it asserts index, channel page, SSE live
#      delivery, and captures README screenshots.
#   5. (Optional, if ANTHROPIC_API_KEY-equivalent claude CLI is available)
#      Run two LLM-driven agents via agent_runner.py and confirm they converse.
#
# All processes are torn down at exit.
set -euo pipefail
cd "$(dirname "$0")"

LOG_DIR=$(mktemp -d)
trap 'kill ${ERGO_PID:-0} ${VIEWER_PID:-0} 2>/dev/null || true; wait 2>/dev/null || true; echo "logs preserved in $LOG_DIR"' EXIT

echo "=== 1. starting Ergo on :17000 ==="
./start-ergo.sh > "$LOG_DIR/ergo.log" 2>&1 &
ERGO_PID=$!
for i in $(seq 1 60); do
    grep -q "now listening" "$LOG_DIR/ergo.log" 2>/dev/null && break
    sleep 0.1
done
if ! grep -q "now listening" "$LOG_DIR/ergo.log"; then
    echo "FAIL: Ergo did not start"
    cat "$LOG_DIR/ergo.log"
    exit 1
fi

echo "=== 2. starting viewer on :8080 ==="
rm -f viewer.jsonl
./start-viewer.sh > "$LOG_DIR/viewer.log" 2>&1 &
VIEWER_PID=$!
for i in $(seq 1 30); do
    if curl -fs http://localhost:8080/ > /dev/null 2>&1; then break; fi
    sleep 0.2
done
if ! curl -fs http://localhost:8080/ > /dev/null 2>&1; then
    echo "FAIL: viewer did not start"
    cat "$LOG_DIR/viewer.log"
    exit 1
fi

echo "=== 3. scripted alice + bob agents ==="
rm -f alice-bot.jsonl bob-bot.jsonl
python3 alice.py > "$LOG_DIR/alice.log" 2>&1 &
ALICE_PID=$!
sleep 0.5
python3 bob.py > "$LOG_DIR/bob.log" 2>&1
BOB_RC=$?
wait $ALICE_PID
ALICE_RC=$?
if [[ $ALICE_RC -ne 0 || $BOB_RC -ne 0 ]]; then
    echo "FAIL: scripted agents (alice rc=$ALICE_RC bob rc=$BOB_RC)"
    cat "$LOG_DIR/alice.log" "$LOG_DIR/bob.log"
    exit 1
fi
echo "  scripted agents ok"

echo "=== 4. Playwright UI test ==="
# shellcheck disable=SC1091
source .venv/bin/activate
python3 playwright/test_viewer.py
PWRC=$?
if [[ $PWRC -ne 0 ]]; then
    echo "FAIL: Playwright"
    exit 1
fi
echo "  Playwright ok (screenshots in screenshots/)"

if command -v claude > /dev/null 2>&1 && [[ "${SKIP_LLM:-}" != "1" ]]; then
    echo "=== 5. LLM-driven agents (claude CLI) ==="
    rm -f alice-bot.jsonl bob-bot.jsonl
    python3 agent_runner.py --nick alice-bot \
        --persona "You are Alice, an agent who likes asking questions about books. Keep replies under 15 words." \
        --max-turns 2 > "$LOG_DIR/alice-llm.log" 2>&1 &
    ALICE_PID=$!
    sleep 1.0
    python3 agent_runner.py --nick bob-bot \
        --persona "You are Bob, an agent who recently read Borges. Keep replies under 15 words." \
        --initial-message "hey, anyone want to chat about a book?" \
        --max-turns 2 > "$LOG_DIR/bob-llm.log" 2>&1
    BOB_LLM_RC=$?
    wait $ALICE_PID
    ALICE_LLM_RC=$?
    if [[ $ALICE_LLM_RC -ne 0 || $BOB_LLM_RC -ne 0 ]]; then
        echo "FAIL: LLM agents (alice rc=$ALICE_LLM_RC bob rc=$BOB_LLM_RC)"
        cat "$LOG_DIR/alice-llm.log" "$LOG_DIR/bob-llm.log"
        exit 1
    fi
    # Confirm both produced at least one channel utterance.
    if ! grep -q "said:" "$LOG_DIR/alice-llm.log" || ! grep -q "said:" "$LOG_DIR/bob-llm.log"; then
        echo "FAIL: at least one LLM agent did not produce a reply"
        cat "$LOG_DIR/alice-llm.log" "$LOG_DIR/bob-llm.log"
        exit 1
    fi
    echo "  LLM agents conversed:"
    grep "said:" "$LOG_DIR/alice-llm.log" | head -2 | sed 's/^/    alice: /'
    grep "said:" "$LOG_DIR/bob-llm.log" | head -2 | sed 's/^/    bob:   /'
else
    echo "=== 5. LLM-driven agents — SKIPPED (no claude CLI or SKIP_LLM=1) ==="
fi

echo
echo "PASS: appendix A end-to-end (ergo + viewer + scripted agents + Playwright UI + (optional) LLM agents)"
