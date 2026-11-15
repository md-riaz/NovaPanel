# Phase / PR Driver Prompt — worker + verifier loop

**What this is.** One reusable prompt that takes a single phase (or a single open PR) from "spec
confirmed" to "merge-ready, CI green, every finding answered" — using a **worker sub-agent** to build
and an **independent verifier sub-agent** to prove it, looping until the verifier returns **CONFORMS**.
It encodes the rules already written in `README.md` (precedence, pre-flight check, pre-review
checklist, the 10 invariants) so the agents don't have to be re-taught them each time.

**Who runs it.** The orchestrating agent on the phase branch. It does **not** write application code
itself; it spawns workers and verifiers, relays blockers to the head, and drives the PR. (The head —
architecture — still owns the spec corpus and answers every raised contract question. "Raise, don't
reinterpret" is absolute.)

**How to use it.** Copy the block below, fill the two placeholders, and run it. Everything else is fixed.

---

## Fill these in

- `{{TARGET}}` — either `phase N` (build it) or `PR #NN` (finish/complete an open PR).
- `{{ISSUE}}` — the tracking issue for the phase/PR (for raising blockers and posting the review request).

Branch is already fixed by the task (`ccr-…`). Never push to `main` or any other branch without explicit
owner permission. Commit footer and PR footer are the session-standard ones; no model identifier anywhere
in commits/PRs/code.

---

## THE PROMPT (copy from here)

> You are driving **{{TARGET}}** to a merge-ready state for NovaPanel. You orchestrate; you do not write
> application code yourself. Work the stages in order and do not skip the verifier.
>
> ### Stage 0 — Orient (you, before spawning anyone)
> 1. Read, in this order and every time: `docs/spec/README.md`, `docs/spec/00-foundations.md` (the F0–F17
>    contract + the `Agent` type, package layout, signatures, config, client), then the target's
>    `docs/spec/phases/phase-N.md`. For a PR target, also read the PR diff and every open thread.
> 2. Pull the canonical anchors the target touches: verb attributes from `01-rpc-and-verbs.md`, args/reply
>    from `03-verb-shapes.md`, tables from `02-data-model.md`, error codes from the **F3 catalog**,
>    cross-cutting rules from the named **F-sections**. These win over any phase prose (README §6
>    precedence). The *why* behind a binding decision is in `05-adr.md`; the prior-art it was weighed
>    against (BHPanel's shipped code, and the other researched panels) is in `vpspanel-plan.md` §1–§4 and
>    the ADRs — consult it when a design choice is in question, never to override a fixed contract.
> 3. Run the **contract-completeness check** (README, six gap-classes) over the phase spec as it stands.
>    If any gap is open (a refusal with no F3 code, an uncovered show-once secret, a web-tier read path
>    that assumes memory, an unpinned throttle, a restated config literal, an unspelled authz exception)
>    → **STOP and raise it on {{ISSUE}}**; do not have a worker guess. The head closes it in the contract
>    first. This is the one-gap-one-patch discipline: close it in the spec, not mid-build.
>
> ### Stage 1 — Decompose (you)
> Turn the target's **Deliverables** into independent work units (by file/feature, respecting the phase
> boundary — never pull later-phase work forward; stub later-owned needs per spec). Write the unit list,
> the canonical anchors each unit must match, and the phase **Guardrails** + the 10 global invariants that
> apply, into a short brief. This brief is the worker's entire context — if you can't write it clearly, you
> don't understand the unit well enough to delegate it.
>
> ### Stage 2 — Build (worker sub-agent(s))
> Spawn a worker (`general-purpose`) per independent unit; parallel units go in one message. Each worker's
> brief says: **build only these deliverables; honor these guardrails; match these exact anchors
> (`NewArgs` struct verbatim from `03`, validate every attacker-controlled name at the sink per invariant
> 4, return only F3 codes, touch only these tables).** The worker runs the **pre-flight contract check**
> (README item 7) before coding and again before handing back, picks the idiomatic Go default for any
> unpinned micro-detail and notes it, and — critically — **raises any contradiction with a fixed contract
> instead of reinterpreting it**. A worker that hits a real spec gap hands back with the gap, not a guess;
> you relay it to {{ISSUE}} and wait for the head.
>
> ### Stage 3 — Verify (verifier sub-agent — independent, read-only)
> When the units are built, spawn a **separate** verifier (`Explore` or `general-purpose`), worktree- or
> read-only-isolated, that did **not** write the code. Give it the diff and the canonical files — **not**
> the worker's conclusions (don't anchor it). It must return a verdict (`CONFORMS` /
> `CONFORMS WITH NOTES` / `DOES NOT CONFORM`) with **file:line evidence** for each of:
> 1. **Full gate green on the head** — `go build ./...`, `go vet`, `staticcheck`, `govulncheck`,
>    `gofmt -l` (empty), `go test ./...`, and the three architecture tests **with their negative
>    fixtures**. A check that passes on warnings or skipped bodies does **not** count (the Phase-1
>    `systemd-verify`/integration lesson) — the gate must exercise the code.
> 2. **The phase's own Verification section passes in full** — it is the definition of done.
> 3. **Contract precedence holds** — verb attributes ↔ `01`; args/reply ↔ `03` (snake_case, documented
>    envelope); every wire `code` ∈ F3 catalog; tables/columns ↔ `02`; each cross-cutting rule **cited
>    from its `00` F-section**, not paraphrased. Any drift between phase prose and a canonical file is a
>    finding (canonical governs).
> 4. **No invariant weakened** — agent boundary, peer gate (SO_PEERCRED), no-shell (argv only, no
>    `sh -c`), every handler a registered verb, default-deny auth, config-sink validate/temp/test/atomic/
>    reload/revert, append-only agent-written audit, agent-sealed secrets, signed anti-rollback updates,
>    web-tier-RCE-non-catastrophic. A green check bought by relaxing a guard is a **blocker**.
> 5. **Scope is this phase only** — no later-phase work; docs/prototype files match `main` unless this
>    phase owns them.
> 6. **Prior-art sanity** — where the phase implements a pattern the researched panels got wrong (the
>    exploited-failure list in `vpspanel-plan.md` §3 and Appendix A), confirm NovaPanel takes the safe
>    path, not the one that bit them.
>
> ### Stage 4 — Reconcile (loop)
> Feed the verifier's findings back to a worker. Fix the root cause (a repeated finding means fix the
> cause, not silence it — never skip/disable/quarantine a test, never relax a guard to go green). Re-run
> the verifier on the new head. Loop until it returns **CONFORMS** (or **CONFORMS WITH NOTES** where every
> note is a deliberate, recorded micro-default). Treat any bot finding (Codex/CodeRabbit/GitGuardian) the
> same way a later PR event would: a **claim to verify**, trace a real caller→failure path, fix if real
> and in proportion, answer with the traced reason if not. Do not merge a big phase while a bot pass is
> still in flight.
>
> ### Stage 5 — PR + drive to green
> Open a **draft** PR (if the target is a phase) or finish the open one, with the **pre-review checklist
> (README) pasted and every box ticked on the current head** and the gate output pasted in. Subscribe to
> the PR. Then follow the standing PR-drive rules: red CI or a merge conflict is work *now*; implement
> small/local review asks and lint-bot fixes and push; reply on every open red-circle thread before a
> fixing push; re-request review after pushing for a changes-requested review. One validated push beats
> three speculative ones — reproduce the failure, show it fixed, re-read the diff adversarially, keep the
> fix minimal, then push. The PR is done only when CI is green on the head, there's no conflict, every
> thread is answered, and (where the repo runs it) Claude Approvals passes. People's approvals/merge are
> the owner's call under the standing merge authority — you verify, the owner's standing authority merges.
>
> ### Stage 6 — Raise, don't reinterpret (always on)
> At any stage, a contradiction with a fixed contract, an invariant you'd have to weaken, or an
> underspecified contract is a **blocker you raise on {{ISSUE}}** — never a judgment call a worker makes.
> The head amends the spec (and, per the "learn from patterns" directive, generalizes the gap-class
> forward into later phase specs + the README check so it's closed once, not re-patched per phase), then
> work resumes.

## (end of prompt)

---

## Why it's shaped this way (for the head maintaining it)

- **Worker ≠ verifier, and the verifier never sees the worker's conclusions.** A verifier handed the
  worker's hypothesis returns it confirmed. Independence is the whole value of the second agent — it's
  what caught the duplicate-`ssl.issue` P1 in the Phase-3 coalesced-domain path that the first-pass
  review missed.
- **The gate is pasted, not asserted.** "CI is green for the right reason" (README) means a check that
  can pass on warnings or skipped bodies doesn't count. The verifier proves the gate exercises the code.
- **Precedence is mechanical, not tasteful.** Canonical file > phase prose, every time. The verifier
  diffs the prose against the canonical anchor and reports drift rather than "looks fine."
- **One gap → one patch, closed in the contract.** Stage 0's completeness check and Stage 6's
  generalize-forward rule exist so the same gap-class is never rediscovered in a later phase. That's the
  "learn from patterns, update the next docs too" directive encoded as process.
- **Prior art is a safety check, not a source of truth.** The researched panels (BHPanel's shipped code
  above all, plus the others in `vpspanel-plan.md`/`05-adr.md`) tell the verifier which failures to make
  sure NovaPanel *doesn't* repeat; they never override a fixed NovaPanel contract.
