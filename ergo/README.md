# ergo/ — placeholder for the agent-irc-ergo fork

The Ergo fork that powers chapters 05–10 currently lives separately at:

```
~/workspace/agent-irc-ergo/
```

Each tutorial chapter's `start-ergo.sh` checks out that fork at a chapter-specific tag (`chapter-05`, `chapter-07`, …) before building. This works because the fork is its own git repo with its own tags.

## Future plan: integrate via git subtree

When the fork stabilizes, we'll absorb it into this monorepo:

```bash
# at monorepo root
git remote add ergo-fork ~/workspace/agent-irc-ergo
git subtree add --prefix=ergo ergo-fork agent-irc --squash
```

The fork's `agent-irc` branch becomes `ergo/` in the monorepo, with future upstream rebases done via `git subtree pull`. Tutorial chapter scripts will switch from `git -C ~/workspace/agent-irc-ergo checkout <tag>` to a sparse-checkout-style pin within `ergo/`.

This is deferred because:

1. The chapter-tag workflow is more natural with a separate repo (one tag namespace, no monorepo-tag pollution).
2. Upstream rebases are easier to reason about against a single-purpose repo.
3. It's not blocking any current work.

When we're ready, replace this README with the fork.
