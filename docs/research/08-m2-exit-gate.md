# M2 exit gate — the honesty milestone

What the milestone's gate is, how to run it, and the map from every normative
behaviour in [issue #21](https://github.com/andrewesweet/tf-mut/issues/21) to the test that
proves it. Written the way `06-m1-exit-gate.md` was, so the M3 spec author can read one
document and know what is established.

## The gate

```bash
mise exec -- just gate
```

Twenty-one cases, six and a half seconds, no network. The recipe names each reproduction
explicitly rather than matching a prefix, and two tests keep that honest:
`TestTheHonestyGateNamesOnlyTestsThatExist` fails if the recipe names a test nothing declares,
and `TestTheHonestyGateCoversEveryNamedReproduction` fails if a reproduction the spec requires
stops being run. A gate that named nothing would otherwise be green.

The closure rule — *M2b and M2c cannot close the milestone while any M2a gate is red* — is
enforced structurally: the gate contains only M2a's reproductions, and it is a separate recipe
from `just test`, so operator and interface breadth cannot hide a failed oracle behind a large
green checklist.

## The oracle's refutations

Each of these is a shape of module for which the obvious implementation reports something
false. They are the milestone.

| Case | What it refutes | Test |
| --- | --- | --- |
| R2-2 | That an identical fingerprint proves unassertability. `terraform_data.derived.input` is unknown at plan time, cty refines it with a known prefix that the plan serialisation discards, and mutating the prefix produces a byte-identical plan document that a `startswith` assertion still kills — verified against Terraform v1.15.8 before the fixture was written | `TestUnknownRefinementSurvivesRatherThanBeingExcluded` |
| The other half | That the conservative rule makes the oracle useless. Over an apply-mode payload with no unknown in it, a mutant nothing can distinguish is excluded and says why | `TestAKnownPayloadLetsTheOracleProveUnobservability` |
| R2-9 | That a volatile value can be masked whole. `"${uuid()}-stable"` keeps its suffix in the fingerprint, so mutating the suffix is a reportable survivor rather than an exclusion | `TestAVolatileTemplateStillYieldsAFindingOnItsStableSuffix` |
| R2-9, converse | That masking is safe to over-apply. `uuidv5` is deterministic, so nothing about it may be masked and its mutations stay killable | `TestADeterministicIdentifierIsNeverMasked` |
| C4 | That the baseline diff can see all volatility. A mutant that selects an impure branch the baseline never takes is undecidable, not maskable: masking it against the baseline would erase the difference the mutation made | `TestVolatilityIntroducedOnlyByTheMutantIsDecidedByARerun` |
| Flake resistance | That "classified once" is the same as "classifies the same". Three consecutive runs of both volatility fixtures produce identical verdicts | `TestVolatilityFixturesClassifyIdenticallyAcrossRepeatedRuns` |
| R2-8 | That overlapping predicates need no precedence. State and diagnosis are identical at one job and at oversubscription, and under a file rename that reverses source order | `TestStateAndDiagnosisAreIndependentOfOrderAndParallelism` |
| C3 | That address intersection can separate a weak assertion from an absent one. A delta observed only through a local and an output must not diagnose `no-assertion` | `TestADeltaSeenOnlyThroughALocalAndAnOutputIsNotNoAssertion` |
| C3, fallback | That the closure always decides. Where a splat projects an attribute the delta is not in, the honest answer is `unasserted`, naming the construct | `TestADefeatedClosureDiagnosesUnassertedAndNamesTheConstruct` |
| C4 (states) | That everything a plan cannot show is noise. `depends_on`, an unexercised `validation` and a precondition are `StructurallyUnassertable`, in the denominator, each naming `expect_failures` as the fix | `TestConstructsWithNoProjectionAreStructurallyUnassertable` |
| C1 | That diagnoses can be assigned in any order. Where two predicates hold, the higher one wins | `TestTheHigherDiagnosisWinsWhenTwoPredicatesHold` |
| Volume | That every mutant can afford a fingerprint. Phase two runs for phase-one survivors and for nothing else | `TestPhaseTwoRunsOnlyForPhaseOneSurvivors` |
| Open question 6 | That a payload can be read without checking its version. A `plan_format_version` outside the pinned range is an operational failure, never a verdict | `TestAPayloadOutsideThePinnedVersionRangeIsAnOperationalFailure` |
| M5 | That the decoder can buffer. See below | `TestVerboseDecodeOfALargeStreamStaysWithinTheHeapCeiling` |
| M5, converse | That a truncated or malformed stream can be classified | `TestTruncatedStreamIsAnErrorRatherThanAnEmptyResult`, `TestMalformedStreamIsAnErrorRatherThanAnEmptyResult` |

## Measurements

### The streaming memory bound

| Decoder | Peak retained heap, four workers, ≥ 100 MB stream | Ceiling |
| --- | --- | --- |
| Streaming, `provider_schemas` skipped token by token | **under 64 MB** (passes) | 64 MB |
| Buffering, one message materialised then filtered | **240 MB** | 64 MB |

Measured with `runtime/metrics` `/gc/heap/live:bytes` — the live heap, not process RSS, which
on a real mutation run is dominated by Terraform and its provider plugins and says nothing
about this decoder. The bound discriminates by a factor of 3.7, so it fails a buffering
decoder by construction rather than by tuning. The input is the recorded real
`terraform test -verbose -json` stream in `internal/tfexec/testdata/verbose-stream.jsonl`, with
its `provider_schemas` members grown to the size a large-provider run produces; manipulating
recorded output is not a fake runner.

### Per-operator error counts

From the applicability-matrix fixture, 222 mutants across 66 operators:

| | Count | Share |
| --- | --- | --- |
| Generated | 222 | — |
| `Invalid` (rejected by `terraform validate`) | 1 | **0.5%** |
| `KilledByError` (caught by Terraform's evaluation) | 17 | 7.7% |

The only operators with a non-zero error rate:

| Operator | Generated | Invalid | KilledByError |
| --- | --- | --- | --- |
| `EXT-LOCAL-NULL` | 29 | 1 | 0 |
| `VAR-DEFAULT-REMOVE` | 8 | 0 | 7 |
| `VAR-DEFAULT-NULL` | 7 | 0 | 4 |
| `VAR-DEFAULT-CHANGE` | 7 | 0 | 2 |
| `CHECK-NEGATE`, `FN-ARG-REORDER`, `PRE-POST-NEGATE`, `VAR-VALIDATION-NEGATE` | 1–2 each | 0 | 1 each |

**Two operators reached this table at 1.00 and were repaired because of it**, which is the
argument for publishing the counts at all. `CHECK-REMOVE` removed a check's only assertion,
which Terraform rejects outright; it now removes the whole `check` block where only one
assertion exists. `OUT-SENSITIVE-FLIP` cleared `sensitive` on an output reading a sensitive
variable, which Terraform also refuses; it is now gated on that evidence. Both repairs moved
the operators from 100% `Invalid` to 0%, and dropped the population's `Invalid` share from
1.3% to 0.5%.

### Cost

The full matrix fixture — 222 mutants over a fourteen-block module, two-phase, offline — runs
in **4.4 seconds** wall clock. The whole offline suite, including three repeated-run
determinism cases and a 222-mutant end-to-end classification, is **21 seconds**.

No real-provider measurement is published here. M1's stands (0.3 mutants/s, 1.08× parallel
scaling against `hashicorp/aws`), and this milestone contains no speed lever that would move
it; the real-provider inner loop is M3's gate, as `product-design.md` §12 says.

## Answering M1's open questions

The M1 implementation review left six open questions for this spec. Five were disposed of by
issue #21; the sixth is answered by data here.

**Open question 3 — what does lazy validation cost in Tiers 1–3?** M1 measured `Invalid` as
nearly unreachable in Tier 0 and warned that "type-changing mutations, weakened validations and
boundary flips error routinely", which would make the M11 lazy-validate win evaporate.

**It does not.** At Tiers 1–3, with the applicability matrix's evidence gates in place,
`Invalid` is 0.5% of the population and `KilledByError` is 7.7%. Lazy validation therefore runs
`terraform validate` for 8.2% of mutants; validating every mutant would run it for 100%, at
~1.7 s each against a real provider. **The recommendation is to keep lazy validation and not to
build selective validation**: the operator-level selectivity M11 originally proposed would save
at most the difference between 8.2% and the share attributable to the three or four operators
that error, which is a fraction of a fraction, and it would add a per-operator policy to
maintain. The matrix's evidence gates are the cheaper control, and they are already the reason
the number is 0.5% rather than 1.3%.

## Contract sweep

Every row of the milestone's two normative tables, and the test that proves it. A behaviour
with no test here is a gap; the two rows that have one are named in *What is not proven* below.

### Aggregate states

| State | Proven by |
| --- | --- |
| `Invalid` | `TestEveryOperatorInTheMatrixFixtureClassifiesThroughTheStateModel` (population), M1's lazy-validate cases |
| `Killed` | `TestRunReportsKilledAndSurvivedOutputs` |
| `KilledByError` | `TestAnExercisedValidationKillsItsMutants`, and M1's discriminator cases |
| `Timeout` | `TestTimeoutLandsInTheDenominatorAndFailsTheGate` |
| `Survived` | `TestRunReportsKilledAndSurvivedOutputs`, and every diagnosis case |
| `StructurallyUnassertable` | `TestConstructsWithNoProjectionAreStructurallyUnassertable`, `TestStructurallyUnassertableSitsInTheDenominator`, `TestAContractFindingSurvivesAPayloadFullOfUnknowns` |
| `Unobservable` | `TestAKnownPayloadLetsTheOracleProveUnobservability` (assignment and exclusion from the scored set) |
| `NoCoverage` | `TestNoCoverageIsAssignedWithoutExecuting` |
| `Ignored` | `TestAReasonedSuppressionIgnoresTheMutantAndRecordsWhy`, `TestIgnoredMutantsLeaveTheScoredSetAndTheGate` |
| Precedence is exclusive and order-independent | `TestStateAndDiagnosisAreIndependentOfOrderAndParallelism` |

### Survivor diagnoses

| Diagnosis | Proven by |
| --- | --- |
| `indeterminate-unknown-values` | `TestUnknownRefinementSurvivesRatherThanBeingExcluded` |
| `indeterminate-volatility` | `TestVolatilityIntroducedOnlyByTheMutantIsDecidedByARerun` |
| `mock-masked` | Negative case only — see *What is not proven* |
| `weak-assertion` | `TestADeltaSeenOnlyThroughALocalAndAnOutputIsNotNoAssertion` |
| `no-assertion` | `TestAnUnreadResourceStillDiagnosesNoAssertion` |
| `unasserted` | `TestADefeatedClosureDiagnosesUnassertedAndNamesTheConstruct`, `TestASplatInsideACallStillDefeatsTheClosure` |
| Exactly one per survivor, by precedence | `TestTheHigherDiagnosisWinsWhenTwoPredicatesHold` |
| Each carries its required evidence | The diagnosis cases above assert their own evidence field |

### Fingerprint contract

| Behaviour | Proven by |
| --- | --- |
| Canonicalisation is deterministic and order-independent | `TestCanonicalisationIsDeterministicAndOrderIndependent` |
| Null, absent and empty are distinguished | `TestNullAndAbsentAndEmptyAreDistinguished` |
| Unknowns carry their addresses | `TestUnknownValuesAreReportedWithTheirAddresses` |
| Component-granular masking | `TestAVolatileTemplateKeepsItsStableComponents`, `TestASpanFromTheSyntaxSurvivesAWholeMaskFromTheRuns` |
| Undecomposable volatility is never identical | `TestAShapeChangeBetweenBaselineRunsMakesTheComparisonIndeterminate` |
| Format versions pinned | `TestAPayloadOutsideThePinnedVersionRangeIsAnOperationalFailure` |

### Configuration, suppression and reporters

| Behaviour | Proven by |
| --- | --- |
| Root-only discovery | `TestConfigurationIsReadAtTheModuleRootAndNowhereElse` |
| CLI overrides the file per scalar | `TestACommandLineFlagOverridesOneConfiguredScalarAndNoOthers` |
| Duplicate blocks are errors | `TestADuplicateBlockIsAnError` |
| Include/exclude conflicts are errors | `TestAnIncludeAndExcludeConflictIsAnError` |
| Unknown settings are errors | `TestAnUnknownSettingIsAnError` |
| Path and resource exclusion | `TestConfiguredPolicyShapesThePopulation` |
| A reasonless directive does not suppress | `TestAReasonlessSuppressionDoesNotSuppress` |
| Reporters merge additively | `TestConfiguredReportersMergeWithTheFlag`, `TestAnUnknownConfiguredReporterIsRefused` |
| Attachment by operator identifier | `TestSuppressionAttachesByOperatorIdentifier` |
| Configuration cannot hide a safety finding | `TestConfigurationCannotHideAnUnmockedProviderFromTheSafetyGate`, `TestConfigurationCannotHideAProvisionerFromTheSafetyGate` |
| Exit codes over the post-suppression population | `TestIgnoredMutantsLeaveTheScoredSetAndTheGate` |
| SARIF result set and levels | `TestSARIFCarriesTheNormativeResultSet`, `TestSARIFLevelsSplitActionableFromIndeterminateSurvivors` |
| SARIF omits suppressed mutants, keeps rejected ones' findings | `TestSARIFOmitsSuppressedMutantsButKeepsRejectedDirectivesFindings` |
| One rule per operator, with catalogue text | `TestSARIFRulesCarryTheCatalogueDescription` |
| SARIF validates against the published schema | `TestSARIFValidatesAgainstThePublishedSchema` |
| All three reporters derive from one report | `TestEveryReporterDerivesFromTheSameReport` |
| Every report validates against schema 2.0.0 | `TestReportSatisfiesThePublishedSchema`, `TestReportValidatesAgainstThePublishedSchema` |

### The operator catalogue

| Behaviour | Proven by |
| --- | --- |
| Every enabled operator has a matrix row | `TestEveryEnabledOperatorHasAMatrixRow` |
| Every matrix row names an enabled operator | `TestEveryMatrixRowNamesAnEnabledOperator` |
| Every enabled operator has a generation site | `TestEveryEnabledOperatorHasAGenerationSite` |
| Every operator classifies end to end | `TestEveryOperatorInTheMatrixFixtureClassifiesThroughTheStateModel` |
| Type-invalid mutants are not generated where decidable | `TestTypeInvalidMutantsAreNotGeneratedWhereTheEvidenceDecides` |
| Identical rewrites are deduplicated across operators | `TestIdenticalRewritesAreDeduplicatedAcrossOperators` |
| The curated function list is closed | `TestTheCuratedFunctionListIsClosed` |
| `VAR-VALIDATION-WEAKEN` emits the validating form | `TestValidationWeakeningEmitsTheFormThatValidates` |
| `FOREACH-TO-COUNT` rewrites every instance-key reference | `TestForEachToCountRewritesEveryInstanceKeyReference` |
| `PROVIDER-ALIAS-SWAP` keeps mock status | `TestProviderAliasSwapKeepsMockStatus` |
| Per-operator error counts are reported | `TestPerOperatorErrorCountsAreReported` |
| The unanswerable-resource warning, over every enabled tier, both directions | `TestAResourceNoOperatorCanMutateIsWarnedAbout` |
| Stable identifiers survive line moves and unrelated edits | `TestIdentifiersSurviveALineMove`, `TestIdentifiersAreStableAcrossRunsAndUnrelatedEdits` |
| Byte-range operators change only the lines they own | `TestByteRangeOperatorsChangeOnlyTheLinesTheyOwn` |
| The source tree is never written to | `TestRunReportsKilledAndSurvivedOutputs`, `TestPreviewCoversTheNewOperatorsWithoutExecutingTheSuite` |

## Reproduction map

Every fixture, and the case it exists for.

| Fixture | Case |
| --- | --- |
| `unknown-refinement` | R2-2: identical plan JSON, killable by `startswith` |
| `oracle` | The conservative rule's other half: apply-mode, fully known, genuinely unobservable |
| `volatile` | R2-9: `"${uuid()}-stable"` and a deterministic `uuidv5` beside it |
| `mutant-volatile` | C4: volatility only under mutation |
| `closure` | C3: a delta through a local and an output, a splat that defeats the closure, and a resource nothing reads |
| `contract` | `StructurallyUnassertable`: `depends_on`, an unexercised `validation`, a precondition |
| `expect-failures` | The contract story's other direction: an exercised validation kills its mutants |
| `precedence-unknown` | Two indeterminacy predicates holding at once |
| `policy` | Configuration, path and resource exclusion, reasoned and reasonless suppression, operator-identifier attachment |
| `operators` | The applicability matrix: a generation site for every enabled operator, and end-to-end classification of all of them |
| `dynamic` | `DYNAMIC-ZERO`'s generation site, previewed rather than run |
| `aliases` | `PROVIDER-ALIAS-SWAP` and its mock-status guard, both directions |

## What is not proven here

Stated rather than left for a reader to discover.

- **`mock-masked`'s true case has no offline reproduction.** The diagnosis fires on an
  apply-mode delta confined to schema-`computed` attributes, and neither offline provider has
  an attribute that is both configurable and computed: `terraform_data` and `null_resource`
  each split cleanly into optional arguments and computed outputs, so no mutation can move a
  computed attribute without also moving a configured one. The false case is proven — a
  configured-attribute delta does not diagnose `mock-masked` — and the true case waits for the
  real-provider fixture M3's gate brings.
- **`DYNAMIC-ZERO` has no end-to-end classification.** A `dynamic` block needs a provider whose
  schema declares a nested block type. Its generation site and mutation text are fixture-backed
  in `dynamic`; its verdict is not.
- **Run-block file splitting and R2-4 are absent by decision, not omission** — measured and
  dropped in `07-m2-cost-model.md`, and the milestone spec removed the closure with it.
