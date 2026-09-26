# M5 exit gate — what implementing issue #152 measured, decided and deferred

Issue #152, revision 5 (the maintainer's ruling of 16 September 2026 removed the OpenTofu
breadth item and carried it verbatim to #166). The milestone ran as five PRs of production
(#168, #169, #170, #178, #183) and seven of measurement and record (#167, #171, #173, #174,
#175, #177, #184), each closing a sub-issue (#153–#164, #176, #172). The measurements are
published in `docs/research/16-m5-01-lifecycle-witnesses.md` through
`docs/research/21-m5-benchmark.md`; this document is the decision record, in the shape
`docs/research/13-m45-exit-gate.md` and `docs/research/15-ddd-structural-exit-gate.md` set.
The implementation review is `docs/reviews/2026-09-26-m5-implementation-review.md`.

## 1. The exit gates, and what ran them

The spec's six exit gates, each recorded met with the evidence that met it. Every test name
in this document is audited by `TestEveryTestNameADocumentClaimsExists`; the M5 gate itself
is audited by `TestTheM5GateNamesOnlyTestsThatExist` and
`TestTheM5GateCoversEveryNamedRequirement`.

**Gate 1 — M5-0 complete: four measurements published, each decision applied under its
stated rule.** Met.

- **M5-0.1 lifecycle witnesses** (`16-m5-01-lifecycle-witnesses.md`): five candidate
  operators measured across shapes (a)–(e). Three admitted — `LC-IGNORE-DROP` and
  `LC-IGNORE-ALL` under shape (b) with (e) as a second witnessed shape,
  `LC-REPLACE-TRIGGER-DROP` under shape (e) only. Two not admitted, rows annotated "no kill
  witness under shapes (a)–(e), Terraform v1.15.8" and reopened on a witness:
  `LC-CBD-FLIP` and `LC-PREVENT-DESTROY-FLIP`. The reachability finding: **no admitted
  operator needs the conditional oracle slice** — at every witnessed shape an admitted
  operator's canonical delta lies on an assertion-reachable path — so the slice was never
  specified; the unadmitted operators do have unreachable-only deltas, and the finding is
  published with nothing in the oracle changed.
- **M5-0.4 corpus census** (`17-m5-benchmark-census.md`): all 23 Oasis repositories pinned
  by commit and archive digest; two complete public invocations per module; the
  module-admission table published with both denominators. The "directly comparable"
  wording was narrowed in the implementing change as the spec required, independent of any
  count.
- **M5-0.3 pack census** (`18-m5-pack-seed-census.md`): 115 loadable candidates from the
  Checkov catalogue, 10 admitted with zero invalid and zero execution-error rates, 105
  recorded `unwitnessed-not-enabled`, none dropped for invalidity. The conditional third
  form operator was licensed: `PACK-WIDEN-CIDR` (#176) admitted on Trivy AWS-0104 (#183).
- **M5-0.5 opportunity census and repair prototype** (`19-m5-opportunity-census.md`,
  `20-m5-repair-prototype.md`): stage 1 found 14 opportunities over 8 of 32 measured
  modules — at or above the ten threshold, so stage 2 ran and measured an **empty cohort**
  (every opportunity refused, no-candidate or operationally unmeasured). The
  **insufficient** branch applies: the counts and costs are published, the repair-yield
  decision stays open, #82 stays open with no build issue, and the reopen condition is a
  corpus revision whose stage-1 census yields a non-empty cohort. The mining rule fired:
  reached 58, fired 0, so the removal proposal was filed as its own issue — #172 — and was
  dispositioned **not planned** with a recorded revisit condition (a corpus where the rung
  is reached and never fires outside the public, validator-shaped stratum).

**Gate 2 — `gate-m5` green with its honesty pair and its offline site witnesses actually
executed.** Met. The gate ran green on this tree at 115 tests, 1 skipped; the skip is
`TestTheStandardReportOfTheMatrixFixtureIsInvariantUnderTheLifecycleOperators`, whose own
skip reason is that this tree *is* the merge-base — the two legs are one commit apart and
run on the pull request. The offline site witnesses executed, not skipped:
`TestEveryAdmittedLifecycleOperatorHasASiteInTheOfflineFixture` and
`TestEveryPackOperatorHasASiteInTheOfflineFixture` pass without the provider mirror, so no
Tier 4 or pack site is witnessed only by a mirror-gated test; the three kill witnesses are
re-executed through the seam by `TestTheIgnoreDropWitnessKillsThroughTheSeam`,
`TestTheIgnoreAllWitnessKillsThroughTheSeam` and
`TestTheReplaceTriggerWitnessKillsThroughTheSeam`. The honesty pair passes: all 63 names in
the recipe resolve to tests, and the recipe covers every requirement the spec names. The
whole-milestone invariance proof
`TestTheStandardReportOfTheMatrixFixtureIsInvariantAcrossTheWholeMilestone` runs and holds.

**Gate 3 — the seeded pack admitted with its witnessed entries published.** Met on the
admitted branch. `security-aws` ships with 11 entries — the 10 the M5-0.3 census witnessed
on `aws-mocked` and the scorable corpus (#178), plus the Trivy AWS-0104 widen-cidr entry
admitted through #176's mechanism (#183) — each carrying its upstream rule identifier and
the catalogue licence. The pack document lists the 105 unwitnessed entries with both
witness counts. The shipped pack is pinned by
`TestTheShippedSecurityAWSPackShipsExactlyTheAdmittedEntries` and witnessed on the real
provider by the integration-tagged `TestEveryEnabledSecurityAWSEntryHasARealProviderWitness`
and `TestEveryWidenCIDREntryHasARealProviderWitness`.

**Gate 4 — the benchmark published under its pinned protocol with both tables, every
pinned module's row outcome and population availability.** Met. `21-m5-benchmark.md`
publishes the module-admission table (both denominators on every rate) and the mutant-level
table in Oasis's units over known populations only, with the pooled score (6,780 scored
mutants over the 8 `scored` modules), the unweighted per-module median (25.0% over 8), the
`Killed`/`KilledByError` split, and the unscored-with-known-population tabulation by row
outcome. Population availability is decided by each module's separate `preview` invocation;
the two-invocation protocol, cold and warm legs, the one operational retry and the verdict
identity across legs are all stated in the document and executed by
`TestTheBenchmarkOverThePinnedCorpus` (#184).

**Gate 5 — #82 closed with scoped amendment, kept open with its build issue linked, or kept
open as insufficient with the counts published.** Met on the **insufficient** branch: #82
remains open, no build issue was filed, and the published counts are the gate's evidence —
14 opportunities (all classified *undecidable constraint*) over 8 modules, cohort size 0,
costs per module in `20-m5-repair-prototype.md`. The preference order in
`docs/design/characterisation.md` §3.2 stands unchanged, which is what the insufficient
branch licenses; the live state of #82 was re-checked (open) when this gate was recorded.

**Gate 6 — implementation review, exit-gate document, M6 handover.** Met. This document and
`docs/reviews/2026-09-26-m5-implementation-review.md` are the record; the M6 handover issue
is filed in #84's shape once this document is final, and its URL is recorded in §7.

## 2. What was measured

One paragraph per measurement; the documents carry the numbers.

| Measurement | Document | Outcome | PR |
| --- | --- | --- | --- |
| Tier 4 kill witnesses, shapes (a)–(e) | 16 | 3 admitted, 2 not; no oracle slice needed | #167 |
| Benchmark corpus census | 17 | 23 repositories pinned; admission table with both denominators | #171 |
| Opportunity census + mining count | 19 | 14 opportunities / 8 modules, all undecidable; mining reached 58, fired 0 | #173, #174 |
| Repair prototype (stage 2) | 20 | empty cohort → insufficient; #82 stays open | #175 |
| Pack seed census | 18 | 10 of 115 admitted, 0 invalid, 105 unwitnessed | #177 |
| Public benchmark | 21 | 8 `scored` of 23 pinned; pooled 6,780 mutants; median 25.0% | #184 |

The two costs the census pair surfaced beyond the spec's predictions: the corpus's previews
were operationally fragile across runs (six modules the census resolved failed preview in
the benchmark run and vice versa, so every mutant-level sum names its unknown populations),
and the pooled score is dominated by the largest modules, which is why the median is
published beside it.

## 3. What was decided

**Tier 4 admission is witness-bounded, and stays so.** The three admitted operators are
`deep`-only, non-projecting, graded against the suite on disk; a freshly characterised suite
reports them `StructurallyUnassertable` with a fix naming an apply-mode run, and the effects
gates are unchanged. The projection changed for no member. `standard` neither includes the
lifecycle operators nor changes its report — proven by the two invariance tests named in
gate 2.

**The pack mechanism ships with origins, and the seeded pack ships with eleven entries.**
Packs are data; the catalogue gains exactly the form operators `PACK-FLIP`, `PACK-REPLACE`
and `PACK-WIDEN-CIDR`. Deduplication is unchanged; every `(operator, pack, entry)` that
produced a survivor's bytes is aggregated onto it as origins, sorted and deduplicated by
`(pack, entry)`, and the red proof disables the aggregation itself —
`TestDisablingOriginAggregationTurnsTheOriginsCaseRed` — because reversing ownership order
must lose no contributor. No pack enters `standard`
(`TestNoPackEntersTheStandardPopulation`); a fresh binary emits no `origins` without a pack
(`TestARealPackReportValidatesAgainstThePublishedSchema` rejects exactly that).

**The schema moved twice, and the second move is a recorded deviation.** 2.4.0 (pack tier,
`origins`) landed as the spec's "one bump for the milestone" (#169); #183's
`PACK-WIDEN-CIDR` then extended the closed operator enumeration again, which under the
repository's own convention is an additive minor bump, so 2.5.0 exists with its consumer
contract documented in the schema description. Both remain published, as do all
predecessors.

**The benchmark's protocol is the pinned one, not a negotiated one.** Cache off, `standard`
tier, no packs, two invocations per module (preview decides population, run decides the row
outcome), cold and warm legs, one operational retry published as a fact about the run, and
the row vocabulary total and stage-ordered. `TestTheRowVocabularyMapsEveryStageSentinel`
holds the vocabulary against the pipeline's stages;
`TestTheBenchmarkAggregationPoolsScoredModulesAndKeepsBothDenominators` and
`TestTheBenchmarkMedianIsUnweightedAcrossModules` hold the two aggregations.

**The mining rung stays; the proposal that could have removed it is closed, not pending.**
#172 was dispositioned with reasons, not left as an open question — the zero is a property
of the measured corpora, removal is a build, and #163's cohort measured empty.

## 4. Contract sweep

Every normative behaviour the spec states, and the test that pins it. Tier 4 first, then the
pack mechanism, the seeded pack, the snapshot and composition rules, the census and
benchmark, and the safety posture.

| Normative behaviour (spec §) | Test |
| --- | --- |
| Admitted operators have matrix rows with witnessed shape and assertion (M5a) | `TestEveryEnabledOperatorHasAMatrixRow`, `TestEveryMatrixRowNamesAnEnabledOperator` |
| Every enabled operator has a generation site, offline (M5a, M5c.1) | `TestEveryEnabledOperatorHasAGenerationSite`, `TestEveryAdmittedLifecycleOperatorHasASiteInTheOfflineFixture`, `TestEveryPackOperatorHasASiteInTheOfflineFixture` |
| Kill witness re-executed: original green, mutant `Killed`/`KilledByError` (M5-0.1, M5a) | `TestTheIgnoreDropWitnessKillsThroughTheSeam`, `TestTheIgnoreAllWitnessKillsThroughTheSeam`, `TestTheReplaceTriggerWitnessKillsThroughTheSeam` |
| Identical fingerprint → `StructurallyUnassertable`, never `Unobservable` (M5a) | `TestALifecycleMutantWithAnIdenticalFingerprintIsStructurallyUnassertable` |
| Module-level and conditional `NoCoverage` keep precedence over lifecycle mutants (M5a) | `TestModuleLevelNoCoverageKeepsItsPrecedenceOverALifecycleMutant`, `TestConditionalNoCoverageKeepsItsPrecedenceOverALifecycleMutant` |
| `deep` includes `standard`; `standard` excludes lifecycle operators (M5a) | `TestDeepIncludesStandardAndStandardExcludesTheLifecycleOperators` |
| `standard` report invariant across M5a and the whole milestone (M5a) | `TestTheStandardReportOfTheMatrixFixtureIsInvariantUnderTheLifecycleOperators` (PR leg), `TestTheStandardReportOfTheMatrixFixtureIsInvariantAcrossTheWholeMilestone` |
| Pseudo-tested count stays over the extreme tier (M5a) | `TestThePseudoTestedCountStaysOverTheExtremeTier` |
| Matrix fixture generates only parseable mutants (M5a) | `TestTheMatrixFixtureGeneratesOnlyParseableMutants` |
| User pack generates, classifies and suggests through `preview`, `run`, `suggest` (M5c.1) | `TestAUserPackGeneratesClassifiesAndSuggestsThroughTheSeam` |
| `origins` present on a collapsed boolean flip, absent without a pack (M5c.1) | `TestOriginsNameThePackEntryOnACollapsedBooleanFlip`, `TestARealPackReportValidatesAgainstThePublishedSchema` (with/without pack) |
| A language operator owns a row a pack entry also produces (M5c.1) | `TestALanguageOperatorOwnsARowAPackEntryAlsoProduces` |
| The red proof disables origin aggregation, not the sort (M5c.1) | `TestDisablingOriginAggregationTurnsTheOriginsCaseRed`, `TestReversingOwnershipLosesNoContributor` |
| Every pack-contract row: malformed file, non-entry block, expression in a literal slot, repeated label, unknown pack, shadowed name, non-boolean flip `from`, `replace` no-op, type-incompatible `to` (M5c.1) | `TestEveryPackContractRowIsRefusedByName`, `TestEveryShippedPackSatisfiesTheUserPackContract` |
| `widen-cidr` fires only on scalar IPv4 CIDRs, never string-internal (M5c.1, #176) | `TestTheWidenCIDRFormFiresOnlyOnScalarIPv4CIDRs` |
| Schema-evidence refusal: an attribute the schema does not describe generates nothing (M5c.1) | `TestSchemaEvidenceRefusesAnUndescribedAttribute` |
| Unsupported attribute form is a no-op in the pack summary (M5c.1) | `TestAnUnsupportedAttributeFormIsANoOpInThePackSummary` |
| A user pack may not shadow a reserved name (M5c.1) | `TestAUserPackCannotShadowTheShippedSecurityAWSPack` |
| Union of flag and configuration, deduplicated by name (M5c.1) | `TestFlagAndConfiguredPacksMergeAsAUnion` |
| Pack flags refused by name on `characterise`, `todos`, `curate`; narrowing refused on `curate`/`--until-dry` (M5c.1) | `TestThePackFlagIsRefusedByNameOnCharacteriseTodosAndCurate`, `TestAConfiguredPackSelectionIsRefusedOnCurateAndUntilDry` |
| Pack wiring through the command line (M5c.1) | `TestPacksAreWiredThroughTheCommandLine` |
| No pack in `standard` (M5c.1) | `TestNoPackEntersTheStandardPopulation`, `TestTheShippedPackSelectionLeavesTheDefaultPopulationAlone` |
| Snapshot rules: edited user pack is a cache miss; stale verified suggestion refused; changed pack forces the full population under `--since`, in or out of the closure (M5c.1) | `TestAnEditedUserPackIsACacheMiss`, `TestAStaleVerifiedPackSuggestionIsRefused`, `TestAChangedPackFileForcesTheFullPopulationUnderSince`, `TestAChangedPackOutsideTheClosureForcesTheFullPopulationUnderSince` |
| Shipped `security-aws` = exactly the admitted entries; resolves unregistered (M5c.2) | `TestTheShippedSecurityAWSPackShipsExactlyTheAdmittedEntries`, `TestTheShippedSecurityAWSPackResolvesWithoutRegistration` |
| Shipped entries witnessed on the real provider (M5-0.3, integration) | `TestEveryEnabledSecurityAWSEntryHasARealProviderWitness`, `TestEveryWidenCIDREntryHasARealProviderWitness`, `TestTheShippedSecurityAWSPackIsWitnessedOnAWSMocked` |
| Schema validated on real reports; `origins` without a pack rejected (schema 2.4.0) | `TestARealPackReportValidatesAgainstThePublishedSchema` |
| Row vocabulary total and stage-ordered (M5d) | `TestTheRowVocabularyMapsEveryStageSentinel` |
| Preview decides population, independently of run (M5d) | `TestTheThreePreviewRefusalsLeaveThePopulationUnknown`, `TestAPreviewThatFailsOperationallyWhileTheRunSucceeds`, `TestTheGatedPreviewFixtureHasAKnownPopulationAndAnUnsandboxedEffectsRow` |
| Both denominators; pooled sums over `scored`; unweighted median (M5d) | `TestTheBenchmarkAggregationPoolsScoredModulesAndKeepsBothDenominators`, `TestTheBenchmarkMedianIsUnweightedAcrossModules` |
| Unscored known populations tabulated by row outcome (M5d) | `TestTheUnscoredKnownPopulationsTabulateByRowOutcome` |
| "Directly comparable" narrowed; limitations stated in document and roadmap (M5-0.4, M5d) | `TestTheBenchmarkDocumentAndRoadmapStateTheComparabilityLimits` |
| Census denominators count JSON-declared variables; JSON stratum unmeasured, never zero (M5-0.5) | `TestTheCensusDenominatorCountsJSONDeclaredVariables`, `TestAnEmptyJSONStratumIsPublishedAsUnmeasured` |
| Opportunity classification: refused candidate / no candidate / undecidable (M5-0.5) | `TestTheOpportunityCensusClassifiesRefusedTypedCandidates`, `TestTheOpportunityCensusClassifiesMissingTypedCandidates`, `TestTheOpportunityCensusClassifiesUndecidableConstraints` |
| Census reading internally consistent across documents (M5-0.5) | `TestTheCensusReadingIsInternallyConsistent` |
| Mining counts split by stratum (M5-0.5) | `TestTheMinedCountsSplitByStratum` |
| Redaction: a secret only in a failed attempt reaches no published artefact (M5-0.5) | `TestTheOpportunityCensusWithholdsRedactedEvidence`, `TestASecretOnlyInARepairFailedAttemptReachesNoPublishedArtefact` |
| Repair prototype pinned before execution, keyed on `todos` constraints, one structured mapping, one retry (M5-0.5 stage 2) | `TestTheRepairCandidateTableIsPinnedAndUsesTodoConstraints`, `TestTheRepairPrototypeUsesTodosForLookupAndStructuredFieldsForMapping`, `TestTheRepairPrototypeMapsOnlyOneStructuredInputAndRetriesOnce` |
| Exactly one `version -json` before a gate refusal (M5-0.5 stage 2) | `TestARepairPrototypeGateRefusalInvokesOnlyVersion` |
| Gate honesty: recipe names resolve; recipe covers the spec's requirements (Testing Decisions) | `TestTheM5GateNamesOnlyTestsThatExist`, `TestTheM5GateCoversEveryNamedRequirement` |
| Documents name only tests that exist (Testing Decisions) | `TestEveryTestNameADocumentClaimsExists` |
| Documented vocabulary matches the binary (Vocabulary) | `TestTheDocumentedVocabularyMatchesTheBinary` |
| Network-gated measurements run the shipped binary over the pinned corpora (M5-0.3, M5-0.4, M5-0.5, M5d) | `TestTheSecurityAWSPackAdmissionMeasurement`, `TestTheBenchmarkCorpusCensus`, `TestTheBenchmarkOverThePinnedCorpus`, `TestTheOpportunityCensusOverThePinnedCorpora`, `TestTheRepairPrototypeOverThePinnedOpportunities` |

The sweep is closed by the three matrix-enforcement tests the catalogue already carried —
`TestEveryEnabledOperatorHasAMatrixRow`, `TestEveryMatrixRowNamesAnEnabledOperator` and
`TestEveryEnabledOperatorHasAGenerationSite` — which is what makes "one row per enabled
operator" true for the three form operators without a new mechanism.

## 5. Built differently from the ticket's words

- **Two schema bumps, not one.** The spec legislated "one bump for the milestone" at 2.4.0;
  the milestone shipped 2.4.0 and then 2.5.0 when #176's `PACK-WIDEN-CIDR` extended the
  closed operator enumeration. The convention (an additive closed-vocabulary extension is a
  minor bump with a documented consumer contract) outranked the milestone's arithmetic, and
  the invariance gate's normalisation already treated the version stamp as governed.
- **The third form operator arrived by its licensed condition, and on time.** The spec made
  `PACK-WIDEN-CIDR` conditional on M5-0.3 finding entries needing it; the census did
  (Trivy AWS-0104), and #176 landed the operator and the entry together after M5c.2's first
  cut rather than inside it. The shipped pack's entry count is 11, not the census's 10, and
  both documents say which census witnessed which.
- **The witnesses used fewer shapes than the spec enumerated.** Shapes (a)–(e) were all
  measured, but every witness came from (b) or (e); (a), (c) and (d) killed nothing. The
  matrix rows name the witnessed shapes, not the enumerated ones, which is what the
  decision rule requires.
- **Stage 2 ran and still decided nothing.** The spec's stage-2 trigger (ten or more
  opportunities) fired, but its decision thresholds never came into play: the cohort was
  empty, so the *insufficient* branch — not the build branch the ≥ 10 trigger might have
  suggested — is the outcome. The two stages' rules are independent, and the record keeps
  them that way.

## 6. What was deferred, with its reopen condition

- **OpenTofu** — removed before implementation by the maintainer's ruling; carried verbatim
  to #166, which is open. The M5-0.2 platform-facts measurement, the tested matrix, the
  flavour block, `gate-tofu` and the `--engine` correction all defer with it; #159 and #154
  are closed as emptied by the ruling.
- **The repair rung** (#82) — stays open on the insufficient branch; reopens when a corpus
  revision's stage-1 census yields a non-empty cohort.
- **The two unadmitted Tier 4 operators** — `LC-CBD-FLIP` and `LC-PREVENT-DESTROY-FLIP`
  re-enter on a kill witness under shapes (a)–(e) on any Terraform version; the rows are
  annotated in the catalogue document.
- **The 105 unwitnessed pack entries** — recorded `unwitnessed-not-enabled` in the pack
  document; a later census over a corpus that exercises them can witness them, and the
  admission rule is unchanged.
- **The other packs** — `security-azure`, `security-gcp`, `capacity`, `compliance` follow
  the same admission path; not started.
- **The `.tf.json` mining stratum** — published as *unmeasured* (no pinned module carries
  root JSON declarations), never as zero; a JSON-bearing corpus revision re-opens it.
- **The benchmark's characterised-first-suite column** — the optional third bucket was not
  run; the gate does not require it and the document says so.

## 7. Flagged for the next review under standing rule 2

Two mechanisms arrived unreviewed and should be re-asked at the next spec review:

1. **The kill-witness admission predicate.** Bounded admission policy — an operator is
   admitted because one executable pair exists under one enumerated shape — was this
   milestone's central product decision. The M5-0.1 document argues it; nothing has
   reviewed the argument. The unadmitted pair and the two corpus-level false-security risks
   (a witness that kills only a vacuous assertion; a shape whose witnessed assertion is
   itself mutant-generated) are the places it would break.
2. **The two-invocation benchmark protocol.** Population from `preview`, row outcome from
   `run`, never inferred from each other, is honest but doubles the corpus invocations and
   produced the cross-run population drift the benchmark document records (six modules
   resolved differently between the census run and the benchmark run). Whether the drift is
   a protocol cost or a corpus fact is unreviewed; the M6 handover carries the question.

And one process observation, recorded here because it is a rule-2 question about this
document: the design-row reconciliation (roadmap M5 paragraph, Tier 4/Tier 5 tables,
`characterisation.md` §3.2, the deduplication paragraph, the `csv` row) was distributed
across the six implementing PRs rather than done in one place, as the spec's precedence rule
assigns it to whichever change makes the claim false. This document verifies the
distribution landed complete; a future milestone that amends the same rows should not
assume one change owns them.

The M6 handover issue, filed once this document was final, is:
**https://github.com/andrewesweet/tf-mut/issues/185**.
