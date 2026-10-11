# ADR-0001: Package-group contexts and lint-enforced dependencies

Status: accepted  
Date: 2026-09-07

## Decision

The bounded contexts are package groups within one Go module, not deployables.
`CONTEXT.md` is the single context map. The dependency direction is enforced by
the configured linter, which landed in #89: core and supporting contexts may
read boundary value types such as `tfexec.RunPayload` and `discovery.Reach` and
supporting value types such as `mutation.Operator`; the boundary never imports
core or supporting; and no context or boundary package imports Publication
(`internal/report`, `internal/skill`), the engine, or the CLI. The application
layer imports every context and Publication; only the CLI imports the
application layer. `internal/report` remains the
stdlib-only publication leaf. The application layer coordinates the contexts
and projects their values into report DTOs.

The #91 use of `internal/sandbox.WriteFreshChecked` by `internal/skill` is the
narrow exception already recorded in `CONTEXT.md`; it does not make
`internal/report` depend on a domain context.

## Alternatives considered

A context map without enforcement was rejected because it records intent but does
not make an import violation fail when introduced. Lint enforcement landed in #89
as depguard rules over the context package globs, test files included, including
the `context-boundary` rule denying Publication, the `hclsyntax-boundary`
rule confining native syntax to `discovery`, `mutation`, `characterise` and
`config`, and the `application-boundary` rule denying the engine and the CLI.
A hand-written architecture test was rejected because that lint
rule will answer the structural question without creating a second rule to
maintain.

## Evidence

This records the bounded-context and dependency-rule decision in [#86](https://github.com/andrewesweet/tf-mut/issues/86), following the finding and recommendation in the [DDD review #85](https://github.com/andrewesweet/tf-mut/issues/85).
