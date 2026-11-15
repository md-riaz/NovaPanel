# AGENTS.md — working rules for agents on NovaPanel

This is the entry point for any agent (or human) working in this repo. Read it, then read
`docs/spec/README.md` and `docs/spec/00-foundations.md`.

- **What this is.** NovaPanel is a single-VPS control panel: an unprivileged `panel-web` talking to a
  root `panel-agent` over a Unix-socket typed RPC. The security model and the build contract live in
  `docs/spec/` — that corpus is the source of truth, not this file.
- **Role split (unchanged).** The architecture head authors and owns the spec corpus (specs, rulings,
  verification gates). Implementing agents build a phase to its spec and make its Verification gate
  pass. **Raise, don't reinterpret:** a spec that is wrong or underspecified is a blocker you raise;
  the head amends the corpus, then work resumes. See `docs/spec/README.md`.
- **Precedence (unchanged).** A canonical file (`01` verb attributes, `02` schema, `03` shapes, the F3
  catalog and F-sections in `00`) **always wins over phase prose.** `docs/spec/README.md` §6 is
  authoritative.

This file adds **one discipline** the corpus structure alone did not enforce, learned the hard way.

---

## Rule the whole lifecycle, not just the create boundary

### The failure mode (what actually happened)

A ruling (`F20`, the MariaDB two-store provenance contract) was written to answer the question that
prompted it: *how does a crash-interrupted `CREATE` roll forward without adopting an unmanaged object?*
That part was correct. But it was validated **only against the create/replay path**. Everything else in
the resource's life was left implicit, and the implementer — correctly building each verb against the
contract — hit the holes one at a time, in separate rounds:

1. `delete` keyed ownership by **name**, so it could `DROP` a *different* operation's object.
2. The lock was keyed to the parent (`db:<id>`) but a MariaDB **username** is a store-global namespace,
   so two databases claiming the same username did not serialize.
3. The object-key lock serialized **execution**, not **admission**, so a `create` queued behind a
   `delete` could resurrect a deleted parent.
4. The replay rule branched on "owned row → re-run", but the provenance row is written one statement
   **before** the object, so an owned-but-`reserved` row with no object wedged the re-run.

Each was a real gap. Each cost a round. None was visible from the create path alone.

### Why it happens (so you can avoid it)

You validate a mechanism against the **sub-problem that prompted it** (usually the happy create path),
not against the **whole lifecycle and the concurrency matrix**. The create path is where your attention
is; delete, rotate, crash-replay, and two-verbs-at-once are where the gaps hide. The fix is not "think
harder" — it is to **run a fixed checklist over every stateful resource before the ruling is confirmed.**

### The lifecycle & concurrency checklist (run it before confirming any ruling/spec)

For every resource a verb **creates or mutates** that (a) lives in a store **not transactional with the
agent's SQLite** — an OS user, a file/dir, a crontab, a firewall rule, a TLS cert, a DNS record, an
object-store repo, a MariaDB object — **or** (b) has a create+delete life and/or a rotatable secret,
state explicitly how each of these is handled. "N/A, because …" is a valid answer; **silence is not.**

1. **Ownership across the boundary.** How do you know an external object is *yours* and not a
   pre-existing unmanaged one? Existence/name alone never proves ownership. Use a **provenance marker**
   (F20) or state the lighter proof and why it holds (e.g. a single-VPS root-owned resource with a
   validated-unique name, where the SQLite row is the proof). **Never adopt** on a bare name match.
2. **Durable identity.** The ownership token is the **stable resource id** (the SQLite row's ULID), not
   the job id. Create, replay, rotate, and delete are **different jobs sharing one resource id** — that
   id, never the name and never a job id, is what threads ownership through the whole lifecycle.
3. **Replay / crash.** The create (or mutate) can die at every step. Write intent **write-ahead**
   (sealed secrets per F12), then make replay branch on recorded state — in particular the window where
   the provenance/intent marker exists but the object does **not** yet. Roll forward (F6), never adopt,
   never silently recreate.
4. **Rotate / update.** Seal the new value **before** applying it; re-publish the claim (F19) from the
   sealed value on replay. If the update spans two stores, **bind both legs into one job** and treat it
   as `done` **only when every store has committed**, so a crash between them rolls forward instead of
   leaving one store stale.
5. **Delete.** Verify ownership by the **durable id, never by name** (a name-only delete can destroy
   another owner's object). Specify: failed-create orphan, name reuse, already-deleted replay, and the
   cascade to children. Delete the external object **before** its provenance marker (so a mid-delete
   crash replays cleanly).
6. **Concurrency.** Lock the **object's own namespace**, not only its parent — a resource whose identity
   is global and distinct from its parent (a DB username, an OS username) needs its own lock, held
   alongside the parent's in a fixed order. And remember the object-key lock serializes **execution,
   not admission**: a verb queued behind a conflicting one must **re-validate its preconditions under
   the lock at execution** (ensure-state) and fail closed, rather than relying on queue order.

### When you write the ruling

- **Trace the resource through all six before you publish.** If you cannot answer one, that is the gap —
  close it now, in the contract, not later in a build round.
- **Update the canonical registry in the same change.** A phase ruling that changes a lock key, a
  schema, or a verb attribute **must** update `01`/`02`/`03` in the same commit. A ruling that lands in
  phase prose but leaves `01` saying something else is a **precedence contradiction** the next
  implementer will raise — this is exactly how `#92`'s dual-lock ruling forced a follow-up (`#93`) to
  sync `01`. The canonical file is the single source of truth; keep it true.
- **Reference the F-sections, don't reinvent them.** F6 (jobs/roll-forward), F7 (ensure-state), F12
  (sealed secrets), F19 (show-once claim), F20 (two-store provenance) already encode most of this. A new
  resource *instantiates* them; it rarely needs new machinery (and new per-feature durability machinery
  is out of scope — see the phase scope-freeze rulings).
- **Far-off phases: mark the gap as owed, don't over-design.** If a resource won't be built for several
  phases (e.g. DNS records in Phase 7), it is legitimate to record *"this is a two-store resource owed
  the full F20 treatment; design it at build time"* rather than specify the whole mechanism
  prematurely — but write the marker so it is not forgotten.

### For implementing agents

This checklist is also a **raise-trigger**. If the ruling for your verb covers create but is silent on
delete-ownership, crash-replay, rotate, or the right lock namespace, **raise it** — do not guess a
lifecycle policy, weaken a check, or drop/adopt an object whose ownership you cannot prove.

---

## Worked example — the MariaDB resource (the reference "complete" ruling)

`docs/spec/00-foundations.md` §F20 + the MariaDB section of `docs/spec/phases/phase-4.md` are the
reference. Mapped to the checklist:

| Dim | How MariaDB does it |
|---|---|
| 1 Ownership | A root-only `novapanel.managed_objects` control schema holds a provenance row per object; **never** `CREATE … IF NOT EXISTS` (that would adopt). |
| 2 Identity | `managed_objects.obj_id` = the SQLite `databases.id` / `db_users.id` (resource ULID), **not** the job id. |
| 3 Replay | Write-ahead the sealed password + names in the job; replay branches on `phase` (`reserved` vs `created`) and on object presence. |
| 4 Rotate | Seal the new password before `ALTER USER`; re-publish the F19 claim from the sealed value; for a WP site the `wp-config` file leg is bound into the same job and re-driven from the sealed value. |
| 5 Delete | `obj_id`-matched (never name-only); provenance-last; handles orphan / name-reuse / already-deleted; `database.delete` cascades over users by their own `obj_id`. |
| 6 Concurrency | Locks `db:<db_id>` **and** `dbuser:<username>` (the global user namespace) in a fixed order; a child create queued behind a parent delete re-validates the parent at execution and fails `precondition_failed`. |

If your resource's ruling cannot fill a row like this (or justify an N/A), it is not ready to confirm.

**The MariaDB two-store lifecycle is now a *closed* contract** (`00-foundations.md` F20 rules 1–9: the
provenance `phase` is the external authority, the SQLite terminal state is ordered against it, and a
startup/periodic reconcile converges every residue). Every inter-store crash point is covered by F6
roll-forward **or** the F20.9 reconcile, proven by the F7 crash-safety replay test across the enumerated
stops. **Do not re-litigate individual crash windows of this class** — a sub-statement timing window the
table + reconcile already converge is covered, with power-loss/VM acceptance deferred (#90). A new
two-store resource *instantiates* this closed contract (fill the table + add its reconcile); raise a
ruling only for a genuine contract gap the table does not cover, not for each crash point.

---

## Build & review cadence (2026-11-06) — one upfront ruling per step, review at completion

Step 1 of Phase 4 cost ~18 reactive ruling rounds because the contract was patched *during* the build,
one crash window per round, and every WIP checkpoint was reviewed. That is the failure mode this file
was written about. From here:

- **One issue per step, with its whole contract + an implementation guide up front.** Before a step is
  built, the head writes **one** upfront lifecycle ruling on that step's issue: run the 6-dimension
  checklist over every stateful/two-store resource the step touches, fill the table (or justify N/A),
  and add a **concrete implementation guide** (the build sequence, files, verbs, and the verification
  rows it closes). The contract is complete **before** the first commit, not patched after. The MariaDB
  reference above is the model; a new resource *instantiates* the frozen F6/F7/F12/F19/F20 contracts.
- **Review at completion, not per-checkpoint.** The implementer builds the **whole step** to its
  Verification gate, self-checks against the upfront ruling + the pre-review checklist, and then requests
  **one** review on the finished step. Do **not** post WIP checkpoints for per-step review, and do **not**
  review them window-by-window — that is what manufactured the reactive rounds. A green Verification gate
  on the finished step is the review trigger.
- **Raise a ruling only for a genuine contract gap.** While building, an implementer raises the head only
  when the upfront contract is actually silent or self-contradictory on something that blocks the build
  (a real `01`/`02`/`03`-vs-prose conflict, a new resource's ownership proof) — **not** for each crash
  window the frozen contracts already converge, and **not** for an adversarial sub-statement timing case
  (that is the deferred #90 acceptance). When in doubt, build to the ensure-state/roll-forward default and
  note the assumption in the PR, rather than stopping for a round.
- **Ship bar.** A step is **done** when its verbs work and its Verification gate (incl. the F7
  crash-replay at the enumerated stops) is green — that is *ship*, not a prompt to hunt the next
  sub-window. Power-loss/VM acceptance stays deferred (#90).
