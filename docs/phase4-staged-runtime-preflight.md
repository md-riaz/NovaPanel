# WordPress staged runtime preflight

## Bounded delegated policy before implementation

Base: reviewed `182cdf6841048c94f96cd49b0a761bd237d4c00a`.

The next dependency is actual staged-artifact custody and verification, not a new
successful isolation callback. The canonical offline trust contract still forbids
PHP/WP-CLI execution until reviewed ini, offline acquisition and site-UID isolation
exist. Implementing an account lock or stopping one FPM pool would not quiesce all
site-UID writers. This slice therefore chooses the requested closest required
staged-artifact verification dependency without pretending it provides quiescence.

A new internal staged preflight opens every path component without following
symlinks, rejects non-root or writable custody, retains regular single-link 0644
artifact descriptors, hashes their actual bounded bytes against canonical pins,
and verifies the detached PHAR signature using only `/usr/bin/gpgv` through the
existing exec runner. The runner gets held parent-descriptor paths, fixed argv,
replacement environment, no stdin secrets and bounded output. Only an exact
successful VALIDSIG for the reviewed fingerprint is accepted. No raw output or
filesystem error is returned. No key discovery, download or PHP execution occurs.

The ZIP must match the official metadata's complete file set and each MD5, reject
unsafe/duplicate/nonregular entries and oversized expansion, and contain the
pinned version.php declaration. Metadata must match its canonical SHA and count.
The exported entry point cannot replace pins or root ownership; scratch positive
fixtures use only a private test seam. Returned public evidence describes this
check, not an execution capability. Held bytes are consumed during preflight;
future execution must retain or re-open and verify custody at its own boundary.

No public verb, production worker or default ConfigIsolation is enabled. Existing
callback revocation, sticky failure, drain and operation recovery remain unchanged.
Actual account/FPM quiescence, trusted root relocation exclusion, staged extraction,
bootstrap/ini custody, DB effects, unchanged runtime official checksum command and
Queue.Run installation remain separate source gaps. Final VM/power-loss acceptance
is deferred and unpassed. No host accounts, services or publication are permitted.

## Acceptance scope

Tests use real scratch files and ZIP decoding, an injected gpgv runner for adverse
responses, and an opt-in exported verifier with the retained official PHAR,
signature, reviewed binary keyring, ZIP and unchanged official JSON. The opt-in
runs actual `/usr/bin/gpgv` via `internal/exec.Real` without executing the PHAR.
The CLI/archive/metadata digests, all 3782 file MD5s and version declaration are
checked afresh. Fixture manifests and ancestor exemptions exist only in the
unexported test seam, never public arguments. Distinct-owner and unprivileged
refusal probes do not create an account.

The first test failed to compile because the verifier was absent. A separate
behavioral RED demonstrated that a ZIP directory/file collision was initially
accepted, then passed after collision checks were added. Other negative probes
cover content tamper, missing/extra/traversal/duplicate/symlink entries, wrong
version, unsafe parent/leaf custody, hard links, writable files, missing files,
nonempty verifier home, signer/exit/status refusal and cancellation. The existing
queue cancellation/reopen/terminal recovery tests remain part of the full suite;
this slice adds no Queue.Run runner and cannot claim installation recovery.

Root-controlled ancestors exclude site writes, not a malicious or relocating
trusted root. No file descriptor held by this preflight survives its return.
The later privileged operation owner must prevent root relocation and preserve
artifact custody through execution, rather than treating evidence as a lease.
Passwords, salts and protected config are never inputs to this API. It writes no
jobs, claims, audit or durable logs and discards raw verifier output.
