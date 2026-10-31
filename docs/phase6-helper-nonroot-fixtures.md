# Helper fixtures under non-root CI

The PR80 helper custody and layout tests previously used `t.TempDir()` as a production installation. Non-root runners own that directory themselves, and common temporary ancestors are writable. Production correctly rejected those fixtures before reaching the sealed-bundle assertions.

The fixtures now exercise the same descriptor-relative directory validator with their effective UID, starting at a held scratch directory. Sealed preparation uses an internal layout callback. Production preparation always supplies `ValidateHelperLayout`, which retains the root-owned, non-symlink, non-writable ancestor walk and supplies owner UID 0 to directory validation. Neither exported entrypoint accepts a policy override.

The tests retain valid sealed preparation, inherited EOF offsets, immutable input, signature failure, mutable bundle refusal, digest failure, no installation side effect and writable binary refusal. Wrong-owner directory refusal runs for every UID. Non-root execution additionally requires both the production layout and preparation entrypoints to reject the user-owned fixture, and directly requires owner UID 0 to reject its held directory.

Focused RED and GREEN evidence is stored outside the repository in `pr80-helper-fixture-evidence` under the agent scratch directory. Root package race tests and UID65534 focused race tests passed before the final gates. An attempted full-package UID-dropping descriptor harness also reached process-spawn and SQLite failures caused by inaccessible host scratch paths and descriptor inheritance. That attempt is not a full non-root suite pass.

Final exact-commit gates and independent review are separate acceptance requirements. This fix does not enable public apply or confirmation, set release trust, add production migration 0007, or claim phase completion.
