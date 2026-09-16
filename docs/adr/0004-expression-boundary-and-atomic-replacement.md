# ADR-0004: Representation-neutral expressions and a shared atomic primitive

Status: accepted  
Date: 2026-09-07

## Decision

`discovery` publishes the representation-neutral `hcl.Expression` contract.
Consumers that genuinely need native syntax use one named, fail-closed accessor:
`discovery.NativeExpression(hcl.Expression) (hclsyntax.Expression, bool)`. A
custom expression model is not introduced. The expression choice remains subject
to the consumer measurement required by #111; this ADR records the target decision
and does not invent measurement results.

Addendum (#116, 2026-09-16): the JSON configuration reader publishes each
declaration as the author wrote it, without re-rendering it into native syntax.
A consumer that genuinely requires the tokens of a JSON declaration re-parses it
at its own point of use through the second and last named accessor,
`discovery.ReparsedNative(hcl.Expression) (hclsyntax.Expression, bool)`, which
fails closed on any spelling that is not one native expression. Characterisation's
typed and mined rungs are its only consumers; the mutation surface and the
static-verdict paths keep `NativeExpression`, so a JSON declaration never reaches
them as native syntax.

The suggestion-apply, characterisation-commit, and skill-install workflows share
only the checked atomic-replace primitive, such as
`internal/sandbox.WriteFreshChecked`. Each workflow keeps its own precondition
set: snapshot and verification evidence for apply, registry ownership and
generated-file rules for characterisation, and user-edit preservation and force
rules for skill installation. The primitive supplies atomic replacement; it is
not a generic write aggregate.

## Alternatives considered

A custom expression model was rejected without a measured consumer that requires
one. If #111's consumer measurement finds such a consumer, that evidence must
reopen this decision through its own issue. One generic write aggregate was
rejected because it would erase the three workflows' distinct snapshot, ownership,
collision, and partial-state invariants. Sharing the checked primitive removes
duplicated filesystem mechanics without merging those protocols.

## Evidence

This records the Terraform representation and write-boundary decisions in [#86](https://github.com/andrewesweet/tf-mut/issues/86), following DDD-4 and the write-protocol recommendation in [review #85](https://github.com/andrewesweet/tf-mut/issues/85).

The consumer measurement #111 required is recorded in
[`docs/research/14-expression-consumer-measurement.md`](../research/14-expression-consumer-measurement.md):
it found no consumer served by neither `hcl.Expression` nor the named accessor, so the
reopen condition above did not fire.
