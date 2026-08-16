# M2 spec review — 16 August 2026

Adversarial review of the M2 milestone spec (issue #21, first body), posted as a comment on
that issue. Scope: the spec only; accepted dispositions, measurements and M1 decisions were
treated as constraints. **Verdict as delivered: "no — this is not ready for an implementation
agent."**

All ten findings accepted, four with modification. The revised spec (issue #21's body after
this review) and the design-document corrections in the same change are the disposition of
record; this file preserves the findings and the reasoning.

## Dispositions

| # | Sev | Finding (compressed) | Disposition |
| --- | --- | --- | --- |
| C1 | CRIT | States and diagnoses conflated and non-exclusive: story 4 listed aggregate states as survivor diagnoses, omitted `indeterminate-unknown-values`, and the predicates overlap (mock-masked ∧ no-assertion; indeterminate ∧ no-assertion) with no precedence | **Accepted.** The spec now carries one normative table: aggregate states (existing precedence) and, within `Survived`, an explicit diagnosis precedence — `indeterminate-unknown-values` → `indeterminate-volatility` → `mock-masked` → `weak-assertion` → `no-assertion`, with `unasserted` as the honest fallback where reachability is undecidable. Every user story now uses that vocabulary |
| C2 | CRIT | The oracle has no implementable soundness contract: "the mutant's evaluated paths" is underivable without the M3 graph, and the fingerprint itself (fields, normalisation, ordering, format-version range) is unspecified | **Accepted, conservative option.** M2's rule: **any unknown value anywhere in any selected run's payload makes fingerprint equality indeterminate** — `Unobservable` requires a fully-known payload. Honest consequence, stated in the spec: plan-mode mocked runs almost always contain unknowns, so `Unobservable` will rarely fire in plan mode and the oracle reaches full power only on apply-mode mocked runs; the conservative direction produces no false proofs. Path-scoped narrowing waits for M3's provenance. A normative fingerprint contract (fields, unknown/null representation, ordering, per-run composition, component-mask encoding) plus pinned `plan_format_version`/`state_format_version` ranges with a negative test are now spec deliverables — disposing product-design open question 6 |
| C3 | CRIT | `no-assertion` vs `weak-assertion` cannot be derived from address intersection: assertions read outputs/locals, deltas live in resources | **Accepted with modification.** A **minimal forward closure through outputs and locals only** is pulled into M2 — a small, bounded subset of the M3 graph, computable from ASTs already parsed. Where expression forms defeat it (splats, whole-object reads, undecidable projections), the diagnosis falls back to `unasserted` rather than guessing. The reviewer's reproduction (delta observed only through a local and an output, diagnosis must not be `no-assertion`) is a mandatory fixture |
| C4 | CRIT | Mutation-introduced volatility unhandled: a mutation can expose volatile paths or mock instances the baseline never observed; the baseline diff cannot mask them | **Accepted.** Disposes product-design open question 3: when a survivor's delta is confined to schema-`computed` attributes or paths the static impure scan (over the **mutant's** AST) marks suspicious, phase two re-runs that mutant once; attributes differing across the two mutant runs are mutant-volatile and masked; if the delta then empties, the fingerprint-identical rules apply; if volatility remains undecidable, the diagnosis is `indeterminate-volatility`. Mandatory reproduction: volatility that exists only under mutation — R2-9's baseline case does not cover it |
| C5 | CRIT | No acceptance contract: exit gate covers three reproductions out of dozens of new public behaviours; "per-operator fixtures" unstated for ~60 operators | **Accepted.** The revised spec maps every public contract to executable acceptance criteria, and the Tier 1–3 **applicability matrix** (per operator: accepted source forms, required type/schema evidence, coordinated rewrites, skip rules, expected error classification) becomes the first deliverable of the operator sub-scope, fixture-backed row by row. `ready-for-agent` retained on the revised body only because the revision adds the acceptance contract the finding demanded |
| M1 | MAJ | `NoCoverage` overclaims: module-level reachability cannot see `count = var.enabled ? 1 : 0`; resource-level claims from module-level evidence | **Accepted.** Claim narrowed to module-level absence, worded as such everywhere; conditional-instantiation analysis stays M3 with the graph. Conservative direction: uninstantiated-in-fact blocks execute and classify by execution rather than being wrongly pre-classified |
| M2 | MAJ | Function operators conflict with M3's metadata-driven catalogue; story 7 promised lifecycle findings while Tier 4 is out; applicability rules absent | **Accepted.** The hard-coded high-signal function pairs are **deliberately** in M2 and the metadata-driven general catalogue remains M3 — the losing document (product design §12/§3a) is corrected in the same change. Lifecycle wording removed from the spec's stories; `validation`/pre/postconditions (Tier 3) carry the structurally-unassertable story. Applicability matrix per C5 |
| M3 | MAJ | Config/suppression semantics incomplete; "reason required" contradicted "missing reason is a warning"; safety-gate interaction unstated | **Accepted.** Semantics now normative: config discovered at the target module root only; CLI overrides file per scalar; duplicate blocks are errors; include/exclude conflicts are errors; reporters merge additively. A reasonless suppression directive **does not suppress** — the finding stands and the directive is reported as rejected. Comments attach to matching operator sites beginning on the following line. Safety inventories are computed before any exclusion is applied, with acceptance cases proving config cannot hide a provider, provisioner or unsevered effect from either gate; `PROVIDER-ALIAS-SWAP` may only swap between aliases of identical mock status (catalogue corrected in the same change) |
| M4 | MAJ | Reporter contracts unspecified: SARIF result set, JSON v2 fields, ID stability, exit codes after suppression | **Accepted.** SARIF result set defined: `Survived` (error level for actionable diagnoses, note for indeterminates), `StructurallyUnassertable` (warning), `NoCoverage` (note); everything else JSON-only. JSON v2 field contract enumerated in the spec; ID = content hash over (operator ID, module-relative path, site content, replacement) — stable under line moves and unrelated edits, broken by file renames, documented as such. Exit-code and incomplete-score semantics restated over the post-suppression population |
| M5 | MAJ | Bounded-memory gate not reproducible: no ceiling, no measurement boundary, whole-process limits conflate Terraform's memory with the decoder's | **Accepted.** Gate specified: phase-two decode of a stream ≥ 100 MB with peak retained engine heap (Go runtime metrics, not process RSS) under 64 MB at four workers; truncated or malformed streams are per-mutant operational failures, never verdicts; the test must fail against a buffering decoder by construction of the bound. Real recorded Terraform output feeds the malformed-stream cases — manipulating recorded output is not a fake runner |
| — | REC | Split into M2a (oracle/states/schema), M2b (operators), M2c (config/reporters); the honesty gate belongs to M2a so breadth cannot hide a failed oracle | **Accepted with modification.** One milestone, one spec (the repo convention stands), but the spec now has three ordered sub-scopes with exactly that content, the honesty gate attached to M2a, and a closure rule: M2b/M2c cannot close the milestone while any M2a gate is red. Ticketing will follow the sub-scope boundaries |

## Pattern note

Round three's lesson, for the spec-writing process rather than the design: the M2 spec
restated the *design* faithfully but under-specified the *contracts* — the reviewer's five
criticals are all "this is not implementable as written", not "this is wrong". A milestone
spec's acceptance criteria must be executable statements about public contracts, and a
catalogue-scale deliverable needs its applicability matrix as a named artefact, not an
adjective. Both are now AGENTS.md-adjacent conventions carried by the revised spec.

## Addendum — 16 August 2026, M3 spec

One clause of the C4 disposition is **retired** by the M3 spec, per implementation decision
M2-2 (`2026-08-16-m2-implementation-review.md`): "a delta that then empties follows the
fingerprint-identical rules". Applied to a path that is volatile only under the mutant, the
clause produces a false proof of unobservability for a mutant an ordinary equality assertion
would kill. The implemented and proven rule is M2-2's: such a path is undecidable, not
maskable, and classifies `indeterminate-volatility`. The rest of the C4 disposition — the
re-run trigger, mutant-volatile masking of paths the baseline already knew volatile, and the
mandatory mutation-only-volatility reproduction — stands unchanged. Recorded here so the
disposition record and the implementation no longer disagree; `product-design.md` §13.3
corrected in the same change.
