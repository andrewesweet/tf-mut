# M3 exit gate — the contract sweep and the closing audit

Milestone M3 ([issue #33](https://github.com/andrewesweet/tf-mut/issues/33), revision 2, and
its sub-scope tickets #44–#54), closed against the spec's own definition of done. The three
exit gates:

1. **The M3a/M3b offline gates are green** — `just gate-m3`, 83 named cases, audited by
   `TestTheM3GateNamesOnlyTestsThatExist` and `TestTheM3GateCoversEveryNamedRequirement` so
   the recipe can neither name a ghost nor quietly drop a requirement.
2. **The M3c real-provider gate is published with both debts settled** —
   `09-m3-real-provider-gate.md`: the pinned inner loop (219 mutants full, 12 selected,
   40.1 s, 13.5× against a 4× portable floor, re-measured after the delivery review's graph
   repair), `mock-masked` withdrawn on measured evidence, `DYNAMIC-ZERO` classified `Killed`
   end to end.
3. **Verdict invariance holds across every lever** — `TestVerdictInvarianceUnderSince`,
   `TestASecondUnchangedRunIsAllCacheHits` (which asserts invariance over the replayed
   population), `TestVerdictInvarianceUnderSampling`, all through the one shared harness.

**The blocking order held.** The commit sequence on the milestone branch is the record:
M3a.1 (#44) → M3b.1 (#45) → M3a.2 (#46) → M3a.3 (#47) → M3b.2 (#48) → M3b.3 (#49) —
completing both offline gates — then M3c.1 (#50), then M3d.1 (#51) and M3d.2 (#52), then
M3e.1 (#53) behind the measurement machinery #50 built, then this closure. No M3d work
predates the M3a/M3b gates; no M3e work predates M3c.

## The contract sweep: every normative behaviour to its test

### M3a — the graph and address soundness (#44, #46, #47)

| Normative behaviour | Test |
| --- | --- |
| One address grammar; graph built once per run from parsed ASTs | `TestEveryGenerationSiteMapsIntoTheGraph` (and `internal/discovery/address.go`'s grammar comment is the documented form) |
| Fail-closed adapter, site end | `TestAnUnmappableSiteFallsBackClosed` |
| Fail-closed adapter, payload end | `TestAnUnmappablePayloadUnknownIsInCone` |
| Forward cone unions same-resource attributes | `TestTheForwardConeUnionsSameResourceAttributes`, `TestOwnResourceUnknownsStayInCone` |
| `terraform graph` supplemental, test-suite-only | `TestGraphAgreesWithTerraformGraphOverTheCorpus`, `TestASeededMissingEdgeFailsTheSupplementalCheck`, `TestNoUserInvocationExecutesTerraformGraph` |
| Path-scoped unknown rule, C2 soundness pair | `TestAnOutOfConeUnknownPermitsPlanModeUnobservable` + the R2-2 reproduction in `just gate` |
| Static `Unobservable`, structurally guarded | `TestTheContractFixtureClassifiesIdenticallyUnderTheShortcut`, `TestStaticUnobservableEqualsTheExecutedVerdict` |
| Conditional `NoCoverage`, C1's three cases | `TestABodyOnlyMutantOfAnUninstantiatedBlockStaysNoCoverage`, `TestAMutantInsideTheConditionExecutes`, `TestAMutantUpstreamOfTheConditionExecutes` |
| Evaluator fail-closed categories | `TestExcludedCategoriesFailClosedToExecution` (target, run.*, unsupported form) |
| Decidable-to-nonzero controls | `TestDecidableToNonzeroControlsClassifyByExecution` |
| Module-level `NoCoverage` unchanged as subset | `TestNoCoverageIsAssignedWithoutExecuting` (M2, still green) |

### M3b — the count levers and the gate table (#45, #48, #49)

| Normative behaviour | Test |
| --- | --- |
| `--since` working-tree union, all arms | `TestAnUncommittedNewResourceIsSelected`, `TestStagedAndUnstagedChangesAreSelected`, `TestACommittedRangeIsSelected` |
| Renames both names; deletions the module | `TestARenameFollowsBothNames`, `TestADeletionSelectsTheWholeModule` |
| Unresolvable situations error | `TestAMissingRefIsAnError`, `TestOutsideARepositoryIsAnError`, `TestAMergeConflictIsAnError`, `TestAShallowCloneLackingTheRefIsAnError` |
| Non-`.tf` classes force the full population | `TestNonTerraformClassChangesForceTheFullPopulation` (one case per class) |
| Deterministic, labelled sampling | `TestSamplingIsDeterministicAndNonAuthoritative`, `TestSinceSelectionIsDeterministic` |
| Cache: coarse correct key, one fixture per dimension | `TestCacheInvalidationPerKeyDimension` (source closure, child module, tests, resolved configuration, environment), `TestMaskedBaselineFingerprintIsAKeyDimension`, `TestTheLockFileIsAKeyDimension`, `TestRemoteModulePayloadsAreAKeyDimension`, `TestAutoVarFilesAreAKeyDimension`, `TestProviderEnvironmentIsAKeyDimension`, and — with a second real adjacent Terraform release, network-gated — `TestTerraformIdentityIsAKeyDimension`. Every named key dimension now has its invalidation fixture |
| Cache refused under the unsafe opt-ins and `--no-cache` | `TestCacheIsRefusedUnderTheUnsafeOptIns`, `TestNoCacheDisablesReadsAndWrites` |
| Disk-format safety, each behaviour | `TestCorruptionIsAMiss`, `TestCacheEntriesAreOwnerOnly`, `TestASymlinkedCacheDirectoryIsRefused`, `TestEvictionIsDeterministicAndSizeCapped`, `TestConcurrentInvocationsShareTheCacheSafely` |
| The gate truth table, row by row | `TestAdoptionThenRegression`, `TestAcceptedSurvivorsPassAndStayInScores`, `TestScopedFailOnNewJudgesTheSelectedPopulationOnly`, `TestScopedMinScoreIsLabelledPartial`, `TestTheCachedRowOfTheGateTable` (staleness not reported, gates partial, write refused with `--no-cache` the remedy), `TestASampledGateIsRefusedWithoutTheOptIn`, `TestASampledFailOnNewIsRefusedWithoutTheOptIn`, `TestBaselineWriteIsRefusedOffTheFullPopulation` |
| Transition cases | `TestAdoptionThenRegression` (killed-regressed), `TestAnAcceptedIndeterminateTurningActionableIsNew` |
| Unobserved distinguished from stale | `TestStaleAndUnobservedAreDistinguished` |
| Baseline suppresses nothing else | `TestBaselineNeverSuppressesSafetyGates`; the incomplete-score marker is untouched by construction (`minScorePasses`) |
| Deterministic exit codes | `TestExitCodesAreDeterministicAcrossTheTable` |
| Verdict invariance, every lever | `TestVerdictInvarianceUnderSince`, `TestASecondUnchangedRunIsAllCacheHits`, `TestVerdictInvarianceUnderSampling` |

### M3c — the real-provider gate (#50)

Network-gated: `TestInnerLoopGateAgainstTheRealProvider` (pinned protocol, the twelve
selected mutant identifiers enumerated literally alongside the four sites, invariance,
factor), `TestTheMockMaskedRefutationHolds` (the withdrawal's evidence), published in
`09-m3-real-provider-gate.md` and `.artifacts/performance/m3-inner-loop.json`.

### M3d — reporters and the Action (#51, #52)

| Normative behaviour | Test |
| --- | --- |
| MTE document schema-valid, pinned version | `TestMTEDocumentValidatesAgainstThePinnedSchema` |
| The score disagreement tested, not denied | `TestTheScoreDisagreementIsAssertedNotDenied` |
| Authoritative metrics embedded and rendered above the viewer | `TestAuthoritativeMetricsAreEmbeddedInTheDocument`, `TestHTMLIsSelfContainedWithPinnedViewerAndLicence` |
| HTML self-contained, licence shipped | `TestHTMLIsSelfContainedWithPinnedViewerAndLicence` |
| JUnit maps every state, vendored dialect | `TestJUnitMapsEveryStateInTheVendoredDialect` |
| Markdown gate outcome and new-versus-accepted | `TestMarkdownCarriesGateOutcomeAndNewVersusAccepted` |
| Action failure order, checksum gate, degradation | `.github/workflows/action-test.yml` (three jobs); the install and run logic lives in `scripts/action-install` and `scripts/action-run` under the shared shell gates and is proven end to end locally; the workflow itself executes on the next push to GitHub |

### M3e — the generated catalogue (#53)

| Normative behaviour | Test |
| --- | --- |
| Opt-in; default population untouched | `TestTheGeneratedCatalogueIsOptIn` |
| Cross-family pairs impossible, `file`→`upper` named | `TestCrossFamilyPairsAreImpossible` |
| `core::` aliases canonicalised, spelling preserved | `TestAliasesAreCanonicalised` |
| Curated identifiers win deduplication | `TestCuratedIdentifiersWinDeduplication` |
| Admission measurement published | `TestAdmissionMeasurementOnTheRealModule` → `m3e-admission.json` and the research document |

## The document-agreement sweep

- **`mock-masked`**: withdrawn everywhere it was normative — the diagnosis constant, the
  oracle branch, the evidence field, the 2.1.0 schema enum, the state table, the diagnosis
  table, the defensibility table. The two remaining mentions in the roadmap section are the
  historical M2 record, which itself anticipated the withdrawal ("or `mock-masked` should be
  withdrawn"). No deviation.
- **The C4 empties clause**: retired before implementation began (spec revision 2 and its
  companion commit); no document still states it. No deviation.
- **The M3e admission decision**: recorded in the matrix (opt-in, not `standard`) and the
  research document (evidence published, decision deliberately not taken). No deviation.
- **Two deliberate deviations, recorded in `AGENTS.md`**: the never-write contract's
  tool-owned exceptions — the project-local `.tf-mut-cache/` directory (M6's "project-local
  default location") and the `.tf-mut-baseline.json` acceptance list, written only on an
  explicit `--write-baseline` over a full fresh population. The testing seam gained its
  third recorded exception: the graph adapters are exercised directly, as issue #44's
  adapter sweep mandates, with every graph-led verdict still asserted through the engine
  seam.
- **Post-review corrections in the closing change**: the cached row of the gate truth table
  — a population served even partly from the cache now refuses baseline writes, reports no
  staleness, and labels its gates partial, as the normative table always said;
  `--allow-sampled-gate` use is now reported (`sampling.gate_opt_in`); and the Action's
  install and run logic moved under the repository's shell gates.
- **Corrections from the adversarial delivery review** (the reject-and-repair round recorded
  on issues #44–#54): provider configuration now makes any cone that touches it unbounded —
  observable, everything in-cone — closing the false-`Unobservable` shape; the conditional
  evaluator reads `terraform.tfvars` and `*.auto.tfvars` in Terraform's own precedence,
  closing the false-`NoCoverage` shape; `--since` includes ignored untracked configuration,
  is NUL-safe for every filename, treats `.tf.json` changes as full-population triggers, and
  fractional samples ceil rather than truncate; the cache key gains auto-var files, the
  provider environment surface, the decoded platform identity, and an enforced-0700
  directory for its whole life; report 2.1.0 retains the withdrawn `mock-masked` vocabulary
  as deprecated so the revision stays additive; JUnit is validated by a validator built from
  the vendored XSD (hierarchy, permitted children, attribute presence and requirements); the
  Action derives SARIF and markdown from one run via `--output`, pins upload-sarif v3, and
  its workflow exercises the failing-run publication path and a genuinely corrupted asset;
  the inner-loop gate pins a 4x factor floor against the published 14.6x and the fixture's
  content digest.

## What remains for M4

The implementation review (`../reviews/2026-08-17-m3-implementation-review.md`) carries the
measurements, decisions and open questions; the M4 handover issue opens the reading list.
