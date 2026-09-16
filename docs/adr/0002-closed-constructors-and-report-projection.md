# ADR-0002: Closed constructors and report projection

Status: accepted  
Date: 2026-09-07

## Decision

Domain invariants live in closed constructors and transitions over unexported
fields. Domain contexts own legal result values; the application layer projects
those values into the existing `internal/report` DTOs at the publication boundary.
Constructors take the evidence required by each outcome, so combinations such as
a diagnosis on a killed mutant are not part of the domain construction API.

Unexported fields alone do not prohibit zero values. The closed construction and
transition API is the control that makes legal values explicit; projection
preserves the current report schema and wire vocabulary.

## Alternatives considered

A type per status value was rejected because it multiplies the model without
adding useful behaviour; a closed constructor set is sufficient. Schema-first
invariants were rejected because publication validation happens too late and is
not a domain construction boundary. Validation functions run after construction
were rejected because invalid states would cross the system before they were
checked.

## Evidence

This records the structural-invariant decision in [#86](https://github.com/andrewesweet/tf-mut/issues/86), following DDD-2 in the [review #85](https://github.com/andrewesweet/tf-mut/issues/85). The Oracle / Verdict context adopted it in #101 (survivor outcomes) and #102 (terminal outcomes, with the population metrics computed over outcomes) and #103 (`oracle.ParseRecord`, the parsing constructor through which a cached verdict and the population arithmetic's recorded outcomes are rebuilt, so a stored record no constructor could have produced is a cache miss rather than a value), and the Suggestion context in #105 (`internal/suggest`'s `Candidate`, `Suggestion` and `Skipped`, projected in `internal/engine/projection.go`), and the Characterisation context in #107 (`internal/characterise`'s `Todo` transitions and the input-provenance vocabulary) and #106 (`Pinned` and `PinSkipped`, the only spellings of a pin, the skipped one taking no expression; both projected in `internal/engine/characteriseprojection.go`) and #108 (`Scaffolded` and `Scenario`, the scaffold promoted only through `Promote` carrying its verification, projected alongside them, after which `internal/characterise` has no production import of `internal/report`). #110 pinned the projection itself: each request type populates only the report blocks its command produces, `report.Report` stays one union document at schema 2.3.0, and `internal/engine/blocks_test.go` asserts per command which of the gate table, suggestion table, apply record and characterisation block are absent. #109 proved the projection total in both directions (`internal/engine/projectionstatus_internal_test.go`: every context status has a wire spelling and every wire spelling is reachable, the `mock-masked` diagnosis and `Evidence.MockResource` the only named exemptions) and closed the depguard rule over `internal/suggest` and `internal/characterise`, test files included. The remaining contexts follow under #86's dependency order.

