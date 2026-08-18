# M4 implementation review

What implementing M4 (issue #58, revision 2; tickets #59–#65) measured, decided and
deferred. The gates and the published measurements are in
`docs/research/11-m4-exit-gate.md`; this document is the decision record the next milestone
spec absorbs first.

## What was measured, and what the measurements corrected

1. **Terraform refuses a constant assert condition.** The suggestion-soundness gate's
   vacuous seed was specified as an "always-true assertion"; Terraform v1.15.8 rejects
   `condition = true` with "The condition expression must refer to at least one object from
   elsewhere in the configuration". The seed is therefore the tautology `<ref> == <ref>` —
   still vacuous, still baseline-green, still caught only by the isolated mutant leg. Rule 3
   (never trust a plausible reading — run it) earned its keep again.

2. **Sensitivity marks do not survive a provider hop.** The sensitivity predicate was
   specified over the payload's own mirrors (`after_sensitive`, `sensitive_values`, an
   output's flag) and their ancestors. Measured against v1.15.8: `terraform_data`'s
   computed `output` carries the value of its sensitive `input` with **no mark of its
   own** — Terraform propagates sensitivity through its expressions, not through a
   provider's computed echo. The path predicate alone would have inlined the secret through
   the unmarked twin. The shipped predicate therefore has a second layer: any string
   rendering the baseline payload marks sensitive anywhere is refused wherever it appears
   (`Payload.SensitiveRenderings`), and the report itself withholds sensitive renderings
   from delta evidence (`(sensitive value withheld)`), so no reporter can carry what the
   suggestion refused to. This is deliberately stricter than Terraform's own renderer.

3. **The cache measurement rejected the per-file key.** Full protocol and numbers in the
   exit-gate document: 3 false reuses out of 8 claimed on the seeded cross-file dependency.
   Recommendation of record for M5: no per-file key; any candidate must be
   graph-dependency-aware and must re-run the pinned protocol first. No finer key exists in
   the tree.

4. **The addressed-collection cut was bracket-blind.** `fingerprint.Address` cut instance
   addresses at the first `]`, so `resource_changes[app[0]]` yielded a nonsense address.
   Latent since M2 — nothing before the address adapter consumed the result of an indexed
   instance precisely enough to notice. Repaired with a depth-aware cut (`cutBracketed`),
   proved by the adapter matrix's `count` and string-keyed `for_each` fixtures.

## Decisions of record

- **The floor tests keep the floor honest after the slice.** M4.0's cases run under a
  `DisableJSONReading` seam control (a deliberate parallel to `DisableStaticShortcuts`), so
  the same fixtures prove both halves: refusal decided from unreadness, and lift decided
  from content. Without the control, landing M4c would have silently converted the entry
  gate's cases into slice cases.
- **JSON reads are all-or-nothing per file.** A `.tf.json` or `.tftest.json` decodes into a
  scratch inventory and merges only after complete success; a file that fails half-way
  contributes nothing. Well-formed content outside the reader's schema
  (`ErrUnmodelledJSON`) keeps that file's floor down — modelled-versus-parseable is the
  distinction that keeps a future Terraform construct from becoming silent permission.
- **JSON-derived closure references are imprecise by construction.**
  `hcl.Expression.Variables` reports which addresses an expression observes and nothing
  about how, so a JSON-mediated read degrades to `unasserted` rather than claiming a
  weak assertion the reader cannot prove.
- **One suggestion per survivor.** The generator emits the first change all three adapters
  admit, or the first refusal's own status. Several assertions per survivor is unmeasured
  value at real verification cost (each suggestion is one full-suite run plus one isolated
  run); the outcome model does not preclude widening later.
- **`--apply` re-verifies in the same invocation.** Selection by ID or `--all-verified`
  operates on the current report's suggestions, freshly verified this run, so the digest
  binding guards the concurrent-editor race rather than a workflow gap; the preflight's
  digest row is proved at its own seam (`apply_internal_test.go`) because no engine-seam
  test can stage that race deterministically.
- **The vacuous-seed correction and the sensitivity second layer** (above) are repairs to
  reviewed findings and are therefore flagged here as candidates for the next adversarial
  review, per standing rule 2.

## The testing seam

One exception added, recorded in `AGENTS.md` alongside the existing three:
`internal/suggest`'s adapter matrices are exercised directly over payload paths and
published provider types (fifteen shapes across addressing and rendering), for the same
reason `internal/fingerprint` is — they are contracts about documents, and the real binary
cannot be driven into each shape on demand. Every adapter outcome is still asserted through
the engine seam in `suggest_test.go`.

## Deferred, and to whom

- **M4.5**: `StructurallyUnassertable` skeleton generation, behind the minable-validation-
  share measurement (C4 relocation, recorded in `characterisation.md`'s sequencing); the
  characterisation skill (`tf-mut-characterise`) joins `skill install` there.
- **M5**: any finer cache key, gated on the rejection rule above; the multi-suggestion-
  per-survivor question, if survivor density ever makes it worth a measurement.
- **Open question**: the suggestion engine renders element assertions only where the
  provider schema types the collection; `terraform_data`'s `dynamic`-typed attributes
  therefore always skip. A typed-fixture provider (the mirror already carries
  `hashicorp/null`) would widen the rendering matrix's positive rows from schema evidence
  rather than from fabricated schemas. Worth a spike before M4.5 leans on rendering.
