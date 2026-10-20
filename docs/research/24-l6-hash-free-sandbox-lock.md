# 24 — L6: the hash-free sandbox lock and the provider-tree guard

What building lever L6 measured, decided and deferred. The
[execution contract](../design/product-design.md#execute) owns lock staging and provider-guard
behaviour. The trial evidence
in `/home/andre/tools/firstmate/data/tfmut-perf-l6-provider-hash-trial/report.md`
(task `tfmut-perf-l6-provider-hash-trial`, 2026-10-19 to 2026-10-20, master `15b79f0` + L1 #267
+ L3 #268 + L2 #269) gated the build; this note records what the implementation added. Evidence
grades follow the trial's: **Direct** = measured or read here, **Supported** = inference from
agreeing direct observations, **Inferred** = reasoned.

## 1. The measurement that gated the build

Terraform recomputes the `h1:` checksum of every locked provider package on every command
(`meta_providers.go:376` at v1.15.8; the check is skipped when the lock records no hashes, and
omitting the argument is the documented way to record none). On the trial's hardware (bravo,
i5-3570K, no SHA-NI) that re-checksum is a fixed 3.3–5.4 CPU-s per invocation, scaling with
provider bytes at about 117 MB/s. A lock file without hashes removed it entirely: an invocation
that runs nothing fell from 3.32 to 0.07 CPU-s, `terraform validate` from 3.65 to 0.42 and from
11.94 to 1.41, and whole runs fell 24–28% on the multi-provider suites with **every verdict
identical** (407 sampled mutants across three suites; 0 state or diagnosis differences; metrics
equal) — all Direct, from the trial. The trial's paired-run prototype (its Appendix B) is this
note's starting point.

The trial also closed the alternatives (Direct): `dev_overrides` saves the same CPU but adds a
warning diagnostic to every validate, makes `init` delete the lock's entries, needs
`TF_CLI_CONFIG_FILE` (which replaces the user's whole CLI configuration), and drops the
locked-version check; an "already verified" location does not exist — Terraform keeps no memory
of a verification, and a copied provider tree costs exactly what the symlinked one does. L4's
per-file scheduling, re-projected against L6's constants, still does not pay (0.89–1.01× of
phase one, survivors paying 8–11% more) — the captain's "build L6, do not build L4" follows
that measurement.

**The saving is hardware-dependent.** On CPUs without SHA-NI it is large and scales with
provider bytes; the L4 report's laptop measured the same checksum at 0.45 CPU-s with SHA-NI, so
there the saving is likely a few per cent (Inferred — not measured on such a machine).

## 2. Implementation and regression evidence

- **Implementation.** [`sandbox.Share`](../../internal/sandbox/sandbox.go) documents the
  `HashFreeLock` opt-in and `Guard`; `materialiseMutant` in
  [`execute.go`](../../internal/engine/execute.go) is the engine's only hash-free opt-in.
  The [execution contract](../design/product-design.md#execute) specifies which paths retain
  the full lock, including every non-mutant staged run.
- **Tests.** The staged-lock document contracts and the guard refusals are exercised directly
  over the package (`internal/sandbox/lock_test.go`, `internal/sandbox/guard_test.go`), under
  the testing seam's recorded exception 9: the real binary cannot be driven into producing each
  document shape on demand, and the report has no surface that observes the staged bytes. The
  documents asserted: the mutant-execution staged lock records no hashes and keeps every locked
  version and constraint; the warm and source locks stay byte-identical; the staged lock shares no inode
  with either; an unparseable warm lock refuses mutant staging; a non-mutant staged lock keeps
  its full bytes, including `h1:` hashes, whether retained from the source or copied from warm-up.
  The guard refusals asserted: growth (size), touch (mtime), removal, and a change behind the
  plugin-cache symlink each refuse a sandbox;
  an unchanged tree and a providerless workspace materialise; a refusal leaves no directory.
  Every verdict these documents lead to is still asserted through the engine seam: the offline
  fixture suite and the provider-mirror fixtures — which install `hashicorp/null` from the
  mirror and exercise the staged hash-free lock on every mutant — run unchanged.

## 3. What the guard bounds, and what it does not

What L6 skips is Terraform's per-command re-verification, **inside mutant sandboxes only**, that
the package about to execute still matches the lock's `h1:` checksum (Supported:
`meta_providers.go:376`). The residual risk is the time-of-check-to-time-of-use window as long
as the run: with a user-level plugin cache, the warm tree's symlinks lead into a directory any
same-user process — including a concurrent `terraform init` — can write. The realistic hazard
is integrity: a swap mid-run would silently change mutant verdicts instead of erroring them.
The guard detects changes visible in size or mtime before sandbox creation. It does not detect
a change after that check and before provider execution, or a same-length, same-mtime forgery.
An attacker able to rewrite the plugin cache can already
replace the Terraform binary or tf-mut itself, so the security delta against that adversary is
unchanged (Inferred, from the trial's security note). Files added to the tree are not stamped —
the lock pins which version path executes, so an addition is not reachable.

## 4. Reproduction

In-repo: `just test` runs the sandbox document contracts and the engine fixtures; the
provider-mirror fixtures need `just tools-install`. The trial's own reproduction is Appendix A
of its report. Beyond the suite, the build was proven against the real binary end to end: warm
`init` against the offline mirror (full lock with `h1:` entries), one stamp, a sandbox staged
through `sandbox.Materialise` (staged lock with no `hashes`; warm lock byte-identical), then a
grown provider binary refused with `ErrProviderChanged` and no directory left behind, then real
`terraform test` in the staged sandbox: 1 passed, 0 failed.

## 5. Limits and what was deferred

One machine without SHA-NI; the SHA-NI-side saving is projected from the L4 laptop's c0, not
measured. The stamp is size and mtime, not content — a content hash would cost a read of every
provider package per run and was not asked for. `TF_REATTACH_PROVIDERS` remains untouched (the
trial marks it speculative: it hands provider process lifetime to the caller). The trial's
recommendation 2 stands as measured: L4 is not built, in any variant.
