# NovaPanel agent instructions

NovaPanel is a Linux hosting control panel written in Go. The spec under `docs/spec/` is
authoritative for schema and behavior; when code and spec disagree, the spec wins, and a real
conflict is a blocker to raise, not a thing to silently resolve in code.

## How coding work is orchestrated

The main session (Opus) only plans and guides: it writes briefs and rulings, picks the slice,
dispatches workers, and makes the merge decision. It does not do the checking itself.

- Read-only checking runs on Haiku: searching, mapping, locating seams, reviews, verifying findings,
  inspecting diffs, and reading gate and CI results. Haiku never edits files.
- Implementation edits run on a codex Orca worker, each edit in its own isolated git worktree, so
  parallel edits never collide.
- Deterministic control flow stays in the workflow script, not in agent prose: branching, which
  path an item takes, and retry limits are code the script decides, not a judgment an agent makes.

## Verify every finding before reporting it

Every finding (a bug, a regression, a review item, a proposed change) gets an independent verifier
agent that did not produce the finding. The verifier tries to refute it. Report the finding only
after it survives that pass; drop it otherwise. This applies to review output, bug hunts, and any
claim that something is broken.

## Fix loops stop after two rounds with no progress

When fixing a finding, stop after two rounds that make no progress and report the blocker instead of
looping. No progress means the same failure (or an equivalent one) survives two fix-and-recheck
cycles. Escalate with what was tried and why it did not move, rather than burning more rounds.

## Merge and branch discipline

- One topic, one branch, one PR. New work starts on a fresh branch off `main`, never piled onto the
  previous topic's branch.
- A PR does not merge until it passes the root-container verification gate (build, vet, untagged and
  lab-tagged tests, go mod verify, govulncheck) and an independent review. Known pre-existing flakes
  (for example issue #130 `TestCreateWorkerReplayAfterAppliedDDL`) are called out, not treated as new
  failures.
- Never add a `Co-Authored-By` trailer to commits or PRs.

## Secrets

Generated credentials never appear in a job result, event, audit record, or plaintext SQLite/WAL.
They surface once through the F19 show-once claim and nowhere else.
