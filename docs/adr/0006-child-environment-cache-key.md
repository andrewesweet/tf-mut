# ADR-0006: The verdict cache keys the child environment, not the process environment

Status: accepted

Date: 2026-10-26

Supersedes: the environment clause of [M3 spec review C4](../reviews/2026-08-16-m3-spec-review.md)
(the [addendum](../reviews/2026-08-16-m3-spec-review.md#addendum--25-october-2026-child-environment-cache-key)
records the amendment; "any doubt is a miss" stands)

## Decision

Compute the child environment once, in `tfexec`. Strip non-forwarded variables
there, and key exactly that value together with tf-mut's own verdict-relevant
in-process reads.

The child environment is the exact environment a Terraform process runs under:
the inherited environment minus the non-forwarded variables, plus the
performance defaults for managed variables the caller has not set, plus the
run's own additions last, resolving duplicate names to their last value
(`internal/tfexec/tfexec.go`). The runner hands it to
the child, and the engine hashes it into the verdict key and the miss
manifest — one computation, so the key and the child can no longer disagree.

The [C4 addendum](../reviews/2026-08-16-m3-spec-review.md#addendum--25-october-2026-child-environment-cache-key)
owns the reviewed strip set and the evidence per name. Stripping applies to
the inherited environment only: a run's additions are explicit per-run input
and enter the key beside the child.

The verdict-relevant in-process reads are `TF_VAR_*` and `TF_CLI_ARGS_test`
(`internal/discovery` consults both beside the run's additions). Both are
forwarded into the child, so keying the child keys them, and neither may ever
enter the strip set. `--since`'s git environment selects mutants but judges
none, and the OTLP endpoint changes no verdict; both stay keyed by
over-inclusion, which costs only misses.

## Alternatives considered

A fixed session denylist at the key (option B's list) was rejected: it
recovered 0 of 6 measured transitions, and the key and the child disagree by
design. A reader allowlist was rejected: provider `ValidateResourceConfig`
calls run under `mock_provider`, so the allowlist must model unenumerable
third-party reads, and one missed toggle replays a stale verdict — the same
hole the M3 round-two re-review refuted. A user-declared ignore list was
rejected: the natural reaction to a miss explanation is to ignore every name
it prints, and the `TF_VAR_strict` demonstration shows one wrong entry yields
wrong Killed verdicts.

## Consequences

- A key/child disagreement becomes impossible, and a stripped variable cannot
  influence any verdict, because nothing that produces a verdict can read it.
- The risk becomes operational: a provider or git needing a stripped name
  fails visibly through the baseline gate, never as a stale verdict.
- The 32-name miss-explanation cap is untouched and noted as a follow-up.

## Evidence

This records the captain's 2026-10-25 decision on the child-environment
design review, whose replay tables the implementation replays in
`internal/tfexec/nonforwarded_test.go` and at the engine seam in
`internal/engine/cache_childenv_test.go`, with the method, inputs and
per-transition results and limits published in the
[child-environment key replay](../research/25-child-environment-cache-key.md).
