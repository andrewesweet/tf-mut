# Build-chain implementation adversarial review — 16 August 2026

A GPT-5.6 Sol reviewer at xhigh reasoning reviewed the complete uncommitted implementation
against base `a924e25`, using independent standards and specification passes. It reported no
critical findings, nine major findings, and three minor findings. This file records the
disposition; repairs are part of the same implementation commit.

## Dispositions

| Finding | Severity | Disposition |
| --- | --- | --- |
| Exact mise pins meant `mise lock --bump` could never update prebuilt tools, and Go module toolchain directives would drift. | Major | **Accepted.** `tools-update` now uses `mise upgrade --bump`, regenerates the Linux lock, and invokes a tested Go helper that synchronizes all four modules. Doctor derives tool expectations from mise rather than stale constants. |
| Provider allowlist, fixture constraint, lock hashes, mirror and generated CLI config were not reconciled. | Major | **Accepted.** A Go verifier now proves exact agreement and authenticates the mirrored provider archive against its `zh:` lock hash. Doctor and both ordinary/security gates consume it. |
| Build-chain policy tests and discovery logic were implemented as Bash test programs. | Major | **Accepted with modification.** Control-plane, lock-integrity and workflow-security assertions moved into Go contract tests; the shell suite now contains only real-Terraform orchestration. Formatter and command adapters remain Bash where their work is direct process sequencing. |
| Mutation timeout returned success even if Gremlins wrote no report. | Major | **Accepted.** A timeout without valid evidence now fails; a valid report emits processed and remaining counts before the report-only success. |
| Codex lacked Claude's protected-edit `PreToolUse` behavior. | Major | **Accepted with modification.** Current official Codex documentation says `permissionDecision: "ask"` is unsupported. Codex now adds explicit pre-edit review context for Bash/Edit/Write; Claude retains its supported ask decision. |
| `just ci` compared only `git status`, missing rewrites to already-dirty files. | Major | **Accepted.** The gate snapshots the full tracked diff plus hashes/modes/symlink targets for every non-ignored untracked source path. |
| R1/R2 engine reproduction fixtures are not present in the bootstrap. | Major | **Deferred to the engine milestones.** The mutation engine entry point does not exist yet, so sandbox inode, classifier and selection reproductions have no executable seam. They remain mandatory milestone fixtures under `AGENTS.md`; this build-chain commit does not claim to implement them. |
| `tools-outdated` does not resolve provider releases and CI lacks an automatic update-PR writer. | Major | **Not accepted as an initial-chain defect.** Provider updates intentionally require a reviewed explicit version; automatically writing branches/PRs needs separate bot credentials and repository policy. Local update/install parity is implemented. |
| Blocking security commands could stop before diagnostic artifacts were generated. | Minor | **Accepted.** Artifact modes now run before their blocking textual counterparts; gitleaks history also writes a redacted JSON artifact. |
| Manifest discovery included ignored local files and skipped symlinked source files. | Minor | **Accepted.** Discovery now uses Git's tracked-plus-non-ignored source set and follows file symlinks while excluding symlinked directories. |
| Spike documentation incorrectly assigned the null provider to fixture C and said locks were ignored. | Minor | **Accepted.** The fixture README now matches the exact locked offline mirror. |

## Additional judgement

The review also noted that a weekly workflow does not run update recipes. The scheduled workflow
is deliberately a verification job: resolving and committing tool updates is an external-write
workflow that requires separate authorization and bot policy. Dependabot remains responsible
only for immutable GitHub Action SHA proposals.
