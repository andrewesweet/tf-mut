# ADR-0001: Package-group contexts and lint-enforced dependencies

Status: accepted  
Date: 2026-09-07

## Decision

The bounded contexts are package groups within one Go module, not deployables.
`CONTEXT.md` is the single context map. The target dependency direction is to
be enforced by the configured linter under #89: core, supporting, and Terraform
Boundary packages do not depend on Publication, the CLI, or renderers;
`internal/report` remains the stdlib-only publication leaf. The application
layer coordinates the contexts and projects their values into report DTOs.

This is a target architecture, not a claim about the current package graph.
`internal/oracle` and later migrations remain future work. The planned #91 use of
`internal/sandbox.WriteFreshChecked` by `internal/skill` is the narrow exception
already recorded in `CONTEXT.md`; it does not make `internal/report` depend on a
domain context.

## Alternatives considered

A context map without enforcement was rejected because it records intent but does
not make an import violation fail when introduced. Lint enforcement is pending in
#89; the configured linter provides the dependency-rule capability needed for
this boundary. A hand-written architecture test was rejected because that lint
rule will answer the structural question without creating a second rule to
maintain.

## Evidence

This records the bounded-context and dependency-rule decision in [#86](https://github.com/andrewesweet/tf-mut/issues/86), following the finding and recommendation in the [DDD review #85](https://github.com/andrewesweet/tf-mut/issues/85).
