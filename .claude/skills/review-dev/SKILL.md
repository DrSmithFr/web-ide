---
name: review-dev
description: Reviews the developments in progress on the kanban of the Web IDE - each ticket In progress, its worktree and branch, its goals and its development conversation of the local assistant - then reports where each one is, what goes wrong, and comments in the conversations. Use when the user asks to check, follow or review the developments, the dev conversations or the tickets in progress.
argument-hint: "[ticket numbers…] [--dry]"
---

# Review of the developments in progress

Read only: you never commit, rebase, kill, resume or answer for the user in a worktree.
Your part is to look, report, and comment in the conversations.

`cwd` of the kanban tools: the main folder of the project (`git rev-parse --path-format=absolute --git-common-dir`, its parent), not a worktree.

## 1. The tickets

`kanban_list`: the tickets **In progress**, and those **To test** with open feedback (a correction may run).
With ticket numbers in the arguments, only those.

For each one, `kanban_get`: branch, worktree, base, goals (done or not), notes, linked conversations.
The conversation to review is the latest one with the role `dev`, `correction` or `resolve`.

## 2. The conversation

`kanban_conversation` with `last: 60` and `tools: true`. Read it again with a larger `last` only if needed. Note:

- **State**: answering now, idle, waiting for the user (questions, plan), or failed ("The answer failed").
- **What it is doing**: the current step of the plan, its latest note to the Orchestrator if it is adopted.
- **Problems**:
  - errors repeated or the same command in a loop;
  - tests failing and left;
  - a step skipped from the plan;
  - a dangerous command: `pkill`/`killall`/`kill` of `web-ide-pod` (it kills the pod of the IDE that runs it), `make service`, `git push`, `git reset --hard`, `rm -rf` outside its worktree;
  - files edited in the main folder instead of its worktree.

## 3. The worktree

With `W` the worktree and `B` the base (`main` when unsure):

```sh
git -C W log --oneline B..HEAD              # its commits (messages start with #<n>)
git -C W status --short | wc -l            # uncommitted files
git -C W rev-list --count HEAD..B          # commits of the base it lacks
comm -12 <(git -C W diff --name-only $(git -C W merge-base HEAD B) HEAD | sort) \
         <(git -C W diff --name-only $(git -C W merge-base HEAD B) B | sort)   # files changed on both sides: conflicts ahead
```

## 4. The goals

Compare the goals with the evidence: commits, tests run in the conversation.
Flag a goal proved but not ticked, and a goal ticked without evidence.

## 5. The report to the user

In the language of the user, one section per ticket:

- **State**: running, idle, waiting for you, failed.
- **Progress**: commits, goals done / total, current step, what is left.
- **Problems**: if any, with the evidence (a message step, a command).
- **Comments**: what you told it.

End with what waits for the user:
- questions to answer;
- failed conversations to resume (their *Resume* button, or `agent_resume` of the Orchestrator);
- the points you kept for them (see below).

## 6. The comments

Send them with `kanban_reply` without asking the user first: they want you to talk to the conversations directly. With `--dry` in the arguments, only propose them.

Keep for the user, without sending, what is theirs to decide: a change of scope or of the plan, a product or design choice, dropping a goal, anything risky to undo. Ask them in the report.

- In English.
- Start with "Review from Claude Code".
- At most five numbered points, concrete: what to do, and why.
- Point at the evidence (a step, a command, a file).
- Nothing when all is fine.

Never use `kanban_answer` for a question that belongs to the user.
