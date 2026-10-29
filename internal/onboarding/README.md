# Safe project onboarding

`onboarding` provides read-only inspection plus explicit `Apply`/`Uninstall` operations. CLI wiring is `oro onboarding plan|apply|uninstall`; apply/removal require `--yes`. It is not wired into planner or executor. Branch enforcement is separately opt-in through `Options{EnableProtection:true}`; absent custom branches, only PRD names `main` and `develop` are used.

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

Linux is the only platform covered by this implementation's tests. macOS, Windows, and Git Bash are not homologated. Use disposable repositories when testing. No hook was installed in a user workspace.
