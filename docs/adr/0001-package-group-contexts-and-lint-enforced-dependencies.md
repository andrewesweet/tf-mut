# ADR-0001: Package-group contexts and lint-enforced dependencies

Status: accepted  
Date: 2026-09-07

## Decision

The bounded contexts are package groups within one Go module, not deployables.
`CONTEXT.md` is the single context map. The dependency policy is: core and
supporting contexts may read boundary value types such as `tfexec.RunPayload`
and `discovery.Reach` and supporting value types such as `mutation.Operator`;
the boundary never imports core or supporting; and no core, supporting or
boundary package imports Publication (`internal/report`, `internal/skill`),
the engine, or the CLI. The application
layer may import every context and Publication; only the CLI imports the
application layer. `internal/report` remains the stdlib-only publication leaf.
The application layer coordinates the contexts and projects their values into
report DTOs.

The #91 use of `internal/sandbox.WriteFreshChecked` by `internal/skill` is the
narrow exception already recorded in `CONTEXT.md`; it does not make
`internal/report` depend on a domain context.

## Alternatives considered

A context map without enforcement was rejected because it records intent but does
not make an import violation fail when introduced. Lint enforces three specific
boundaries, test files included. The pre-existing #89 `context-boundary` rule
denies `internal/report` imports from core, supporting and Terraform Boundary
packages. This change adds `hclsyntax-boundary`, which permits `hclsyntax` imports
only in `discovery`, `mutation`, `characterise` and `config`, preserving those
packages' native parsing and walking, and `application-boundary`, which denies
the engine and `cmd/` from core, supporting and Terraform Boundary packages.

Boundary imports of core or supporting contexts, imports of `internal/skill`,
Publication's own dependency direction and report's stdlib-only constraint remain
policy; these lint rules do not enforce them. A hand-written architecture test
was rejected because the configured linter answers the structural questions it
enforces without creating a second rule to maintain.

## Evidence

This records the bounded-context and dependency-rule decision in [#86](https://github.com/andrewesweet/tf-mut/issues/86), following the finding and recommendation in the [DDD review #85](https://github.com/andrewesweet/tf-mut/issues/85).
