---
name: tf-mut-development
description: Use when changing Go code, Terraform fixtures, build-chain files, CI, or agent adapters in tf-mut; routes work through the shared Just gates and fixed real-Terraform seam.
---

# tf-mut development

Read `AGENTS.md` and the applicable accepted design before editing.

For product behavior, start with a failing externally observable test. Exercise the engine seam
with configuration in, report out, and the real Terraform binary; do not add a fake Terraform
runner. Keep source modules immutable and operate on sandbox copies.

Use the narrowest Just recipe during red/green work. Run `just fmt` or `just lint-fix` only when
the requested work authorizes source rewrites. Before completion, run `just ci`; run
`just security` separately when dependency, security, build-chain, or release behavior is in
scope. Use bounded `just fuzz` and report-only `just mutate-diff` for focused diagnosis.

Never invoke networked or credentialed Terraform implicitly. `just test-integration` and
`just test-performance` require explicit intent and `TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1`.

Record minimized property/fuzz failures as committed regressions. Preserve fixed seeds in the
ordinary gate and include the emitted random seed when reporting scheduled failures.
