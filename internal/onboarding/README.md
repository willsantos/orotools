# Safe project onboarding

`onboarding` provides read-only inspection plus explicit `Apply`/`Uninstall` operations. CLI wiring is `oro onboarding plan|apply|uninstall`; apply/removal require `--yes`. Branch enforcement is separately opt-in through `Options{EnableProtection:true}`; absent custom branches, only PRD names `main` and `develop` are used.

## Planner/executor/init wiring (M04.3/M04.6)

- Recipes can declare adoption with a step `type: onboarding` carrying optional `protect_branches` (explicit names only, never inferred). The planner runs the read-only `PlanApply` preflight against the plan base and embeds the resulting sealed plan plus its actions/conflicts in `planner.Plan.Onboarding`; the dry-run preview lists files, actions and conflicts. The executor applies exclusively through the embedded sealed plan (`Apply` verifies the HMAC seal and re-inspects); conflicts, drift and inspection failures block only that step (recoverable), never overwrite anything, and never abort the rest of the recipe. Nothing is written by inspection alone and `--dry-run` performs zero writes.
- `oro init --onboarding [--protect-branches b1,b2] [--dry-run]` is the explicit opt-in for adoption after the manifest write: it plans, previews and applies only with `--yes`. Declined or blocked onboarding leaves init successful with the manifest intact. Without `--onboarding`, init behavior is unchanged and installs nothing.
- The `rails` recipe demonstrates the recipe-level opt-in with an `onboarding` boolean variable defaulting to `false` (`oro apply --set onboarding=true`).

Safety contract:

- Project scope only. No global Git configuration and no Node/npm requirement.
- `Inspect` performs no writes and resolves top-level, Git dir, common dir, and effective `core.hooksPath`. Any configured value, including empty/global/worktree config, blocks installation.
- Repositories with linked worktrees are blocked because Git shares hooks via common metadata. Symlinked managed paths and forged/stale `Plan` destinations are rejected; apply/uninstall recompute locations.
- `.gitignore` receives a versioned exact block while retaining existing file mode. Ownership state records original existence, bytes, mode, installed digest, and versioned checksum over original metadata. State validation checks encoding, invariants, backup-to-installed relationship, and current installed type/mode; malformed/tampered state or drift blocks apply and uninstall. Restoration retains exact original bytes, mode, and absent-file status.
- These checksums detect isolated corruption/inconsistent edits; they are not authentication. A local actor able to rewrite all state fields, checksums, and managed files is outside this integrity guarantee. Where ownership cannot be established consistently, onboarding refuses removal.
- Hook ownership and mode are validated. Existing hooks are never overwritten. No Husky/custom adapter is provided.
- With protection opt-in, `pre-commit` blocks direct commits to configured names and `pre-merge-commit` blocks merge commits to same branches. Git limitations: `--no-verify` bypasses hooks; fast-forward merges do not invoke `pre-merge-commit`; hooks are local and do not govern server-side merges/pushes. CI and remote branch protection remain authoritative.
- Shell hooks compare branch names as safely quoted literal data; branch names are validated by Git. Reapplying identical options is a no-op. Changing protection or branch list against existing ownership state fails explicitly; uninstall intact state first, then opt in with desired configuration.
- Apply/uninstall preflight managed files and use atomic replacement per file. Rollback touches only exact bytes written by current operation and reports rollback failures. There is no crash-atomic transaction across multiple files; concurrent external writers can race after preflight, so callers must serialize onboarding operations.

## Platform matrix (M04.7)

| Platform | Status | Evidence |
|---|---|---|
| Linux | validated locally (behavioral tests) | disposable `t.TempDir()` repositories, hermetic, no network |
| macOS | compile-verified only — NOT homologated | `GOOS=darwin go build ./...` |
| Windows / Git Bash | compile-verified only — NOT homologated | `GOOS=windows go build ./...`; `GOOS=windows go vet ./...` carries only the pre-existing `internal/manifest` test failure (`syscall.SIGXFSZ`) |

Linux validation exercised (unit/integration level, disposable repos): Git dir/common dir/worktree detection; linked-worktree block; `core.hooksPath` block at local, global, worktree and empty scopes; symlink ancestry rejection; forged/tampered plan and ownership-state rejection (fail closed); drift blocking apply and uninstall; exact `.gitignore` round-trip (absent file, CRLF, missing trailing newline) with mode preservation; managed hook mode drift; preflight rollback without partial ignore writes; shell-injection-safe branch names; real hook enforcement via `git commit`/`git merge --no-ff` including `--no-verify` bypass; idempotent re-apply; uninstall preserving local drift; planner/executor sealed-plan flow (preview, dry-run zero writes, tampered-seal refusal, conflict blocks) and the `oro init --onboarding` matrix (opt-out identical, clean adopt, `--protect-branches`, dry-run, conflicts, non-Git dir, re-init no-diff).

Untested surfaces on macOS / Windows-Git Bash (declared not homologated, not merely deferred):

- Hook execution semantics: shebang `#!/bin/sh` resolution and shell behavior under Git Bash / Windows Git.
- `core.hooksPath` resolution on Windows: path separators, drive-absolute and relative forms, case-insensitive filesystem interactions.
- File-mode and permission semantics (POSIX mode bits are emulated/absent on Windows); managed mode validation may behave differently.
- Atomic replacement via rename onto existing files and symlink semantics (Windows symlink creation requires privileges).
- Path separator and case-sensitivity behaviors across `.gitignore`, hooks dir and ownership state paths.
- Process-liveness/can-verify paths of adjacent local-locking subsystems (non-unix fallbacks), which share the same untested-platform caveat.
- Linked-worktree and `core.hooksPath` behavior as exercised through the Windows Git implementation.

Cross-compile results (2026-10-29, Go toolchain of this repo): `GOOS=windows go build ./...` and `GOOS=darwin go build ./...` pass; `GOOS=windows go vet ./...` fails only on the pre-existing `internal/manifest` test (`syscall.SIGXFSZ` undefined on Windows), identical to the base commit.

Linux is the only platform covered by this implementation's tests. macOS, Windows, and Git Bash are not homologated. Use disposable repositories when testing. No hook was installed in a user workspace.
