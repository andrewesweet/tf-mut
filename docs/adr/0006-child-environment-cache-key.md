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
run's own additions last (`internal/tfexec/tfexec.go`). The runner hands it to
the child, and the engine hashes it into the verdict key and the miss
manifest — one computation, so the key and the child can no longer disagree.

The non-forwarded variables are the git location variables plus session
bookkeeping: trace-context propagation (`TRACEPARENT`, `TRACESTATE`), shell
bookkeeping (`SHLVL`, `OLDPWD`, `_`), multiplexer and terminal identity
(`TMUX`, `TMUX_PANE`, `TERM_PROGRAM`, `TERM_PROGRAM_VERSION`), systemd
per-invocation identifiers (`INVOCATION_ID`, `SYSTEMD_EXEC_PID`,
`MEMORY_PRESSURE_WATCH`, `MEMORY_PRESSURE_WRITE`, `JOURNAL_STREAM`), SSH
session identifiers (`SSH_CLIENT`, `SSH_CONNECTION`, `SSH_TTY`,
`XDG_SESSION_ID`), and tf-mut's own `TF_MUT_*` namespace. Stripping applies to
the inherited environment only: a run's additions are explicit per-run input
and enter the key beside the child. The C4 addendum carries one line of
evidence per name.

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
per-transition results published in
[`docs/research/25-child-environment-cache-key.md`](../research/25-child-environment-cache-key.md):
every recorded transition replays with a published false-reuse count of 0.
