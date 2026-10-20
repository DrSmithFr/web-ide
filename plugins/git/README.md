# Git plugin

The Git tool and the git content of the diff view. It provides the `worktrees` and `git` capabilities. A built-in plugin: [plugins.md](../../docs/design/plugins.md); `plugin.json` is its manifest.

- **Pod** (`*.go`, package `git`): The `git.*` methods; *Start development* of the kanban needs its `worktrees`. Git itself runs through `pod/git`.
- **Page** (`web/`, the chunk `plugin-git`): The tabs, tree of changes, graph lanes `graph.ts`, commit detail; the git state `state.ts`, which is the `git` service; the git diff source `diff.tsx`.
- **E2e** (`e2e/`, run by `e2e/run.sh`): git.

The page module imports the plugin API only (`@ide/api`, `@ide/types`, `@ide/capabilities`); the Go package imports the core of `pod/`.
