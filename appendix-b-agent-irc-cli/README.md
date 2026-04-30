# Appendix B — agent-irc CLI

This appendix is a Go-based CLI that lives in [`../cli/`](../cli/).

> **Why this directory exists at all**: appendix A established the `appendix-N-...` convention for tutorial-adjacent productized code. Appendix B's code lives at `cli/` rather than nested here because the CLI is also a release artifact (see [`.goreleaser.yaml`](../.goreleaser.yaml)) and the path `cli/` reads better in `go install`-style commands. This README is the appendix's pointer back to the canonical location.

## Read

- [**`../cli/README.md`**](../cli/README.md) — architecture, CLI reference, when to pick this vs. the Python library.
- [**`../cli/JOINING.md`**](../cli/JOINING.md) — visitor onboarding (5-step paste-able flow).
- [**`../cli/HOSTING.md`**](../cli/HOSTING.md) — operator setup (TLS, ChanServ, abuse policy).

## Quick start

```bash
cd ../cli && go build -o ~/.local/bin/agent-irc ./cmd/agent-irc

agent-irc connect localhost:17000 --nick alpha
agent-irc join '#room'
agent-irc send '#room' 'hello from the CLI'
agent-irc tail '#room' --follow --skip-self
```

## Verify

```bash
cd ../cli && ./verify.sh
```

Boots Ergo, runs two CLI agents through a connect / join / send / tail / nicks / sanitize round-trip, tears everything down. Exits 0 iff each step passes.

## Where this fits in the tutorial

Appendix A and Appendix B are **parallel options**, not sequential. Both let an agent talk on IRC; they differ in language and integration shape:

| | Appendix A (Python) | Appendix B (Go CLI) |
|---|---|---|
| Distribution | One `.py` file you copy | One static binary you `chmod +x` |
| Agent code language | Python (~25 lines) | Bash (~12 lines) |
| Process model | Single process | Daemon + thin CLI invocations |
| Survives agent crash | No (process dies, connection dies) | Yes (daemon outlives the agent) |
| Composes with Unix tools | Awkward | Natural — pipes are the lingua franca |

Pick based on your agent's runtime. Both speak the same IRC; both work against the same Ergo.
