# AGENTS.md — agent guide for the agent-irc tutorial

This file is for AI agents (Claude, Codex, etc.) working on the agent-irc tutorial repo. It captures the *non-obvious* things — how to drive an interactive TUI like weechat through tmux, what verification each chapter expects, and which claims in the READMEs an agent can/can't validate.

If you're a human, you can read this too. It's just opinionated about its audience.

## Repo layout

```
~/workspace/
├── agent-irc/             ← this repo (the tutorial)
│   ├── 01-hello-irc/      ← Part I: from-scratch toy server (Go)
│   ├── 02-channels/
│   ├── 03-keepalive-isupport/
│   ├── 04-retiring-the-toy/   ← Part II: pivot to Ergo
│   ├── 05a-capability-negotiation/   ← exploration only, no fork
│   ├── 05b-vendor-capability/        ← first fork modification
│   ├── 06-sasl-and-account-tag/
│   ├── 07-custom-sasl-erc8004/
│   ├── 08-gating-on-the-registry/
│   ├── 09-identity-binding/
│   ├── 10-authorization-lifecycle/
│   ├── README.md
│   └── AGENTS.md          ← you are here
│
├── ergo/                  ← upstream Ergo (chapter 04 builds this read-only)
└── agent-irc-ergo/        ← our fork, branch `agent-irc` (chapters 05b, 07–10 modify it)
```

If `~/workspace/ergo` or `~/workspace/agent-irc-ergo` is missing, follow the "Repository layout" section in the top-level README to set them up.

## How to verify chapters

Most chapters have a `verify.sh` that exits 0 on success. **Run that first**; it's the canonical correctness check. The interactive recipes in the READMEs are pedagogical — they let a human poke at the protocol — but the verify scripts cover the ground truth.

```bash
cd <chapter-dir>
./verify.sh
echo "exit=$?"
```

Expected:

| Chapter | What verify.sh proves |
|---|---|
| 01 | TCP handshake → 001 RPL_WELCOME via netcat |
| 02 | 35 ircdocs/parser-tests cases pass; alice/bob exchange PRIVMSG via #room |
| 03 | 5 runtime steps: ISUPPORT, casemapping collision, ping timeout, ping reply, broadcast |
| 04 | smoke test against unmodified Ergo |
| 05a | **no verify.sh** — this chapter's deliverable is muscle memory; run `./start-ergo.sh` and walk the recipe |
| 05b | `agent-irc.example/hello` is in CAP LS, REQ-able, ACK'd |
| 06 | 4 phases: REGISTER, SASL PLAIN reconnect, anonymous bob, account-tag asymmetry |
| 07 | ERC8004 SASL: positive + negative (sig mismatch) cases |
| 08 | registry membership gate: registered → 903, unregistered → 904, sig mismatch → 904 |
| 09 | registry name becomes IRC nick; invalid names → 904 |
| 10 | cross-chain replay rejection + mutation watcher KILLs |

## The weechat-via-tmux pattern

For chapters 03 / 05a / 06's "Watching it interactively in weechat" sections, you can verify the *protocol claims* with the verify.sh scripts (or with `nc` directly), but verifying *what the user sees in weechat* needs a real terminal. As an agent without a TTY, use `tmux` as the terminal emulator and `capture-pane` to read the rendered grid.

### Recipe

```bash
# 1. Start a detached tmux session running weechat with its own config dir
#    (so it doesn't clash with the user's running weechat).
tmux kill-session -t tut 2>/dev/null
tmux new-session -d -s tut -x 120 -y 30 "weechat --dir /tmp/weechat-tut"
sleep 2

# 2. (Optional) Start the chapter's Ergo before the next steps.
cd <chapter-dir>
nohup ./start-ergo.sh > /tmp/ergo-tut.log 2>&1 &
ERGO_PID=$!
for i in $(seq 1 50); do
    grep -q "now listening on" /tmp/ergo-tut.log && break
    sleep 0.1
done

# 3. Drive weechat: send commands, sleep briefly, capture.
tmux send-keys -t tut "/server add agentirc localhost/16671 -notls" Enter
sleep 0.4
tmux send-keys -t tut "/connect agentirc" Enter
sleep 1.5

# 4. Snapshot the rendered pane (current viewport, no escape codes).
tmux capture-pane -p -t tut

# 5. For more rows including scrollback:
tmux capture-pane -p -S -50 -t tut

# 6. Cleanup.
tmux kill-session -t tut
kill $ERGO_PID 2>/dev/null
fuser -k 16671/tcp 2>/dev/null    # kill anything stuck on the port
```

### What works

| Action | tmux command |
|---|---|
| Start weechat | `tmux new-session -d -s NAME -x 120 -y 30 "weechat --dir /tmp/weechat-NAME"` |
| Type a slash command | `tmux send-keys -t NAME "/cmd args" Enter` |
| Capture viewport | `tmux capture-pane -p -t NAME` |
| Capture scrollback | `tmux capture-pane -p -S -N -t NAME` (last N rows incl. history) |
| Switch to buffer N | `tmux send-keys -t NAME M-N` (Alt+N) |
| Page up in current buffer | `tmux send-keys -t NAME PageUp` |
| End / Home | `tmux send-keys -t NAME End` / `Home` |

### Timing (learned the hard way)

| Operation | Minimum sleep |
|---|---|
| Between `/quote` slash-commands sent in succession | **1.5s** — anything less and weechat starts dropping the next one (input buffering interacts with screen redraws). 0.7s is too short. |
| After `/connect` before sending any CAP commands | 2.5s — let the welcome sequence finish first |
| Before `tmux kill-session` (final cleanup) | 1.5–2s — `kill-session` is synchronous and will preempt in-flight TCP writes between weechat and the server |

**Tip**: when verifying claims about wire behavior, read Ergo's debug log (`/tmp/ergo-*.log` after `start-ergo.sh`) rather than `tmux capture-pane`. The debug log is the source of truth — it shows what actually hit the server. Weechat's display can be ahead of, behind of, or different from what got transmitted, especially around CAP renegotiation and scroll-mode interactions.

```bash
# After running a recipe, check ergo's wire log:
grep "userinput\|useroutput" /tmp/ergo-CHAPTER.log | tail -30
```

### What doesn't work (or is unreliable)

| Claim | Reality |
|---|---|
| `Alt+R` opens the raw buffer | Doesn't fire reliably from `tmux send-keys M-r`. Use `/server raw` (the command form) instead — it always works. |
| Visual color rendering (highlight nicks, status bar colors) | `capture-pane -p` strips colors. Use `-pe` if you need ANSI escapes; reading them is on you. |
| Mouse interaction | Don't try; tmux mouse forwarding to weechat is its own headache. |
| Reading the entire scrollback of a busy buffer | `capture-pane -S -1000 -p` works but truncates to whatever rows weechat has rendered. For exhaustive history, use `/buffer copy` or `/print` to dump to a file weechat-side. |

### Sizing

`-x 120 -y 30` is a reasonable virtual-terminal size for weechat. Smaller and you get aggressive line wrapping that mangles pattern-matching; larger and your captures get unwieldy. Match the README screenshots' geometry roughly to 120×30.

## Verifying interactive recipes

When a README says "you'll see X in weechat," and you want to confirm it actually does, here's the pattern:

```bash
# Run the recipe via tmux, capture, grep for the claim.
tmux new-session -d -s tut -x 120 -y 30 "weechat --dir /tmp/weechat-tut"
sleep 2
tmux send-keys -t tut "/server add toy localhost/16671 -notls" Enter
tmux send-keys -t tut "/connect toy" Enter
sleep 1.5
tmux send-keys -t tut "/server raw" Enter
sleep 0.5

# Confirm the claim
out=$(tmux capture-pane -p -S -100 -t tut)
echo "$out" | grep -q "IRC raw messages" || echo "FAIL: raw buffer didn't open"
echo "$out" | grep -q "agent-irc.example/hello" && echo "vendor cap visible" || echo "vendor cap missing (expected for 05a, present for 05b)"

tmux kill-session -t tut
```

This is the pattern to use when you want to *prove* a recipe works, not just *describe* it.

## Things you cannot verify

| | Why |
|---|---|
| **Visual layout** ("the bracket lines line up nicely") | `capture-pane` returns a 2D character grid, not a rendered image. Box-drawing chars survive but if the user's terminal uses a proportional font for non-ASCII, drift happens client-side and you can't see it. |
| **IDE markdown preview rendering** | The user's IDE is not in the loop. If something looks "ugly" in their preview, you won't know. |
| **Color or attention-grabbing UI** ("the highlighted nick stands out") | Same — capture is uncolored by default. |
| **Real human typing speed / interaction patterns** | If the recipe says "now type three messages quickly to see fakelag," the speed of `tmux send-keys` may or may not provoke the same effect. |

When the user reports "this looks broken," ask for a screenshot or a description; don't trust your own capture to reflect what they see.

## Common pitfalls

- **Port conflicts.** Each chapter's start-ergo.sh listens on a fixed port (16670 → 16676 across chapters). If a stray Ergo from a prior session is still running, `fuser -k <port>/tcp` first.
- **Stale tmux sessions.** `tmux kill-session -t tut 2>/dev/null` at the start of every recipe. Tmux sessions persist across your tool calls if left running.
- **Stray weechat processes locking the config dir.** Use `--dir /tmp/weechat-tut` with a unique path per session; never let tmux-spawned weechat write to `~/.weechat/` (the user's real config).
- **`go build` on Ergo needs `GOTOOLCHAIN=go1.26.2`.** The `start-ergo.sh` scripts already set this; if you `go build` Ergo manually, set it yourself or `auto` will fail looking for `1.26.0`.
- **Ergo's data dir.** `start-ergo.sh` wipes `./data` on every run — accounts and channel state DON'T persist across runs. If a chapter's recipe assumes Alice's account is registered, register it explicitly within the same run (see chapter 06's "Step 0").

## Test-the-tutorial checklist

Before considering changes to the tutorial committed-and-tested:

```bash
# 1. Each chapter's verify.sh exits 0
for d in 0[1-9]*-* 1[0-9]-*; do
    [[ -x "$d/verify.sh" ]] && (cd "$d" && ./verify.sh > /dev/null 2>&1 && echo "$d ok" || echo "$d FAIL")
done

# 2. (Optional) Walk through the interactive recipes in chapters 03, 05a, 06
#    via the tmux pattern above; confirm the claims they make.

# 3. Top-level cross-references resolve (no dead chapter links)
grep -rn '\(\.\./0[0-9]\|\(./0[0-9]' README.md 0*/README.md 1*/README.md | \
    awk -F'[()]' '{print $2}' | sort -u | while read p; do
    [[ -d "$p" ]] || echo "MISSING: $p"
done
```

## When in doubt

- **Verify.sh failing** → look at the verify.sh source and trace what it tests against the actual chapter code. Most chapters are independent; nothing should cascade.
- **Recipe claim differs from reality** → run the recipe via tmux (above), get the actual capture, fix whichever side is wrong (usually the README, since the code has tests).
- **README seems out of sync with the fork** → `cd ~/workspace/agent-irc-ergo && git log --oneline` shows what changed per chapter. Chapter 05b through 10 each have one commit there.
- **User reports "ugly rendering"** → it's almost certainly unicode box-drawing chars (`┌──┐`, `│`, `┐│┘`) drifting in a proportional-fallback font. Replace with plain ASCII (`+--+`, `|`, etc.). See commits `81b165a` and `92ba59c` for prior examples.

## Memory

The user's auto-memory at `~/.claude/projects/-home-lin-workspace-agent-irc/memory/` already has:

- Project context: agent-irc gates registration on ERC-8004, uses on-chain name as IRC display name
- Deferred work: a CLI signer (`agent-irc-sign`) for chapter 07 that lets any IRC client do ERC8004 SASL via `/quote` + a separate signing tool. Mentioned to lin; not yet shipped.

Keep that memory updated when scope decisions change.
