# ADR-0001: Package-group contexts and lint-enforced dependencies

Status: accepted  
Date: 2026-09-07

## Decision

The bounded contexts are package groups within one Go module, not deployables.
`CONTEXT.md` is the single context map and owns the
[full dependency policy](../../CONTEXT.md#dependency-direction), including the
narrow exception for `internal/skill`. Lint enforces a subset of that policy.
The application layer coordinates the contexts and projects their values into
report DTOs.

## Alternatives considered

A context map without enforcement was rejected because it records intent but does
not make an import violation fail when introduced. The configured
boundary rules cover test files too. See
[dependency direction](../../CONTEXT.md#dependency-direction) for the enforced
edges and the policy constraints that remain unenforced.

A hand-written architecture test was rejected because the configured linter
answers the structural questions it enforces without creating a second rule to
maintain.

## Evidence

This records the bounded-context and dependency-rule decision in [#86](https://github.com/andrewesweet/tf-mut/issues/86), following the finding and recommendation in the [DDD review #85](https://github.com/andrewesweet/tf-mut/issues/85).
