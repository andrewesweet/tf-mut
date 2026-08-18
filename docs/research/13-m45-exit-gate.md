# M4.5 exit gate — what implementing characterisation measured, decided and deferred

Companion to `docs/reviews/2026-08-18-m45-implementation-review.md`. Terraform v1.15.8, Go
1.26.6, 18 August 2026.

The contract sweep below maps every normative behaviour in issue #71 to the test that proves
it. Where a behaviour was not built, or was built narrower than the spec's words, it says so
here rather than in a commit message.

## 1. The gates, and what runs them

| Gate | Recipe | Cases |
| --- | --- | --- |
| M2a honesty | `just gate` | unchanged |
| M3 offline | `just gate-m3` | unchanged |
| M4 offline | `just gate-m4` | one substitution, below |
| **M4.5 offline** | **`just gate-m45`** | 43 named cases, audited by name |
| M4.5-0 measurement | `just measure-synthesis` | network-gated, publishes its own decision |

The M4 gate's `TestACheckBlockInJSONRetainsTheFloor` is retired and
`TestAMovedBlockInJSONRetainsTheFloor` takes its place. The check block's file is now *read*,
so the old name would be a lie; the protection it stood for — an unmodelled top-level JSON
construct leaves the file unread and the floor down — is proved with a construct that is
still unmodelled.

## 2. Platform facts measured in this slice, not assumed

Standing rule 3 says never to trust a plausible reading of Terraform behaviour. Four readings
were run.

1. **`removed` blocks accept destroy-time provisioners, and `check` blocks accept exactly one
   scoped data source.** Both validate on v1.15.8. Two data blocks in one check is
   `Multiple data resource blocks`, which is why the check fixture uses two check blocks.
2. **`state_key` isolates a generated scenario's state, and an assertion-less `run` block is
   legal, passes, and still yields the whole mocked state through `-verbose -json`.** The
   harvest surface is `test_state.outputs` and `test_state.root_module.resources[]`, each
   resource carrying `values` and `sensitive_values`.
3. **Terraform reports *every* failed assertion of a run, each as its own diagnostic with its
   own source range, in declaration order and independent of which failed first.** This is
   the M7 platform fact, and it is the reason kill-set participation is read directly rather
   than reconstructed by isolating assertions one at a time. No indeterminate-participation
   status was needed. Proved in the slice by `TestOneMutantFailingTwoAssertionsAttributesBoth`
   over a fixture pair with the declaration order reversed.
4. **Terraform refuses an assert whose condition references nothing from the configuration**
   (`The condition expression must refer to at least one object from elsewhere in the
   configuration`). This shaped the curate fixture: an assertion with a genuinely empty kill
   set has to read *something*, so the fixture's reads the test's own input.

## 3. The typed-fixture provider spike, concluded

The M4.5-0 spike asked for schema-typed collection cases proven end-to-end via the mirrored
`hashicorp/null`, **or** for the `configured` rung to document the measured skip classes. The
second is what the evidence supports. Characterising the aliased-provider fixture at
`--pin configured` produces exactly four classes, measured:

| Class | Example | Why |
| --- | --- | --- |
| `pinned` | `terraform_data.anchor.input == "steady-dev"` | a primitive the configuration determined |
| `skipped-mock-invented` | `null_resource.primary.id` | schema-computed: the value came from the mock |
| `skipped-unrenderable` (nested) | `null_resource.primary.triggers.env` | the payload path cannot tell a map key from an object attribute |
| `skipped-unrenderable` (typed null) | `terraform_data.anchor.triggers_replace` | the payload's `null` has lost its type |
| `skipped-volatile` | `terraform_data.anchor.id` | a fresh UUID per run, caught by the double run |

No schema-typed *collection* pin is generated, and that is the M4 rendering contract
holding rather than a gap: `hclwrite.TokensForValue` renders a list and a set identically and
`toset(["a"]) == ["a"]` is false, so a collection equality would be generated and refuted.
The ladder documents the classes instead of guessing.

## 4. The synthesis-rate measurement, and its decision

Published in full in `docs/research/12-m45-synthesis-rate.md`. Nine of the nine readable
corpus modules yield an executable default scenario with no TODO answer; the median module
has zero open judgement points; the decision therefore applies in the ship-as-specified
direction and `--answer` stays a repeatable per-identifier flag.

Two costs the number carried with it, both flagged for the next review:

- **Validation mining fired zero times across the whole corpus.** 605 of 609 resolved inputs
  came from the module's own default and 4 from typed synthesis. The design's unquantified
  caveat is confirmed. Mining stays — it costs little and it is the only rung that reads a
  constraint as a *value* — but no product claim may rest on it.
- **Refusing `moved` costs one corpus module in ten.** `terraform-aws-modules/eks` v20.8.5 is
  not characterisable at all. The refusal is the C4 disposition of record and is not reversed
  here, under standing rule 2. The reviewer's attention is drawn to the asymmetry: `import`
  names a provider configuration and reads a real resource at plan time, which is the R2-10
  fail-open shape, while `moved` is state bookkeeping with no provider, no effect and no
  evaluation. The two were disposed of as one construct.

## 5. Measurements this slice produced about its own behaviour

- **The until-dry loop converges in one round on every fixture tried, with zero new pins.**
  This is not the loop failing: the harvest already pins every value the M4 rendering
  contract can express at the chosen rung, so there is nothing left for the
  survivor-suggestion path to add at the same rung. The loop's value is in modules whose
  scenarios leave a branch unpinned, and in survivor deltas expressible where the harvested
  value was not. `TestUntilDryConvergesWithoutWritingAByte` proves the loop runs against the
  staged overlay — over a module with *no test directory at all*, so a baseline would refuse
  for want of run blocks if discovery had not consumed the overlay — and that the source tree
  is byte-identical afterwards.
- **Reorder invariance is structural under state-payload pinning.** Each run's state
  converges to its own configuration, so a scenario's harvested values do not depend on what
  ran before it. The distinct `state_key` per scenario is the guard that keeps it that way
  when the generated files are merged, and `TestScenarioPinsAreInvariantUnderFileOrder`
  asserts identical pins across three stagings: one file per scenario, one shared file
  forward, one shared file reversed.

## 6. Contract sweep

| Normative behaviour (issue #71) | Proved by |
| --- | --- |
| `check` and `removed` reach both inventories, both syntaxes | eight cases in `constructs_test.go` |
| `moved`/`import` refused in both readers | `TestAMovedBlockIsRefusedInHCL`, `TestAnImportBlockIsRefusedInHCL`, `TestAMovedBlockInJSONRetainsTheFloor` |
| the effective staged suite is what the gates judge | `TestAnUntestedAliasedProviderModuleCharacterisesWithNoOptIn` |
| a mock per provider *configuration* | `TestAMissingAliasMockRefusesBeforeExecution` |
| effects gate unchanged and unfooled | `checkStagedSafety`, exercised by the provisioner fixtures |
| distinct `state_key` per scenario | `TestScenariosCarryDistinctStateKeys` |
| scenario-reorder invariance | `TestScenarioPinsAreInvariantUnderFileOrder` |
| input synthesis in the preference order | `TestTodosListsTheOpenJudgementPointsWithTheirEvidence`, the corpus measurement |
| TODO material is a non-executable artefact class | `TestAnUnsynthesizableInputBecomesANonExecutableArtefact` |
| `todos`, `--answer`, `--resume`, promotion | `TestAnAnsweredTodoIsPromotedAndTheSuiteIsGreen`, `TestTheTodoSurfacesAreWiredInBothArgumentOrders` |
| double-run volatility exclusion | `skipped-volatile` in `TestTheConfiguredRungPinsOnlyWhatTheConfigurationDetermined` |
| pinning through the M4 renderer and sensitivity machinery | `TestASensitiveValueReachesNoGeneratedArtefact` |
| redaction from the first failed attempt onwards | `TestASecretInAFailedAttemptReachesNoArtefact` |
| the zero-output contract, both halves | `TestAZeroOutputModuleEscalatesAndSaysSo`, `TestARungThatPinsNothingIsNeverComplete` |
| the write protocol and the input-closure digest | `TestAWrittenSuiteIsGreenAndRegistered`, `TestASecondWriteIsRefusedAsACollision`, `TestForceReplacesOnlyUnmodifiedGeneratedFiles` |
| shell and file contract; post-path flags refused by name | `TestCharacteriseIsWiredThroughTheCommandLine`, `TestCharacteriseRefusesArgumentsAfterTheModulePath`, `TestTodosRefusesArgumentsAfterTheModulePath` |
| the staged suite; converges without `--write` | `TestUntilDryConvergesWithoutWritingAByte` |
| `--until-dry` ladder-respecting | `TestUntilDryRespectsTheGranularityLadder` |
| `curate` refuses partial populations at configuration time | `TestCurateRefusesAPartialPopulationAtConfigurationTime` |
| eligibility by provenance; report-only | `TestCurateReportsAnEmptyKillSetWithItsEvidence`, `TestCurateWritesNothing` |
| kill-set participation measured | `TestOneMutantFailingTwoAssertionsAttributesBoth` |
| `expect_failures` scaffolds non-executable | `TestUnassertableConstructsBecomeNonExecutableScaffolds` |
| the characterisation skill under the install protocol | `internal/skill` suite, extended to both skills |
| the falsifiable walkthrough | `TestTheInstalledSkillsWalkthroughExecutes`, `TestASeededWrongFlagInTheSkillTurnsTheGateRed` |
| report-2.3.0 validated on real reports | `TestARealCharacterisationReportValidatesAgainstThePublishedSchema` |

## 7. Built narrower than the spec's words — say so here

Three places. None of them is hidden behind a passing test.

1. **Scaffold promotion is emission-only.** `expect_failures` scaffolds are generated, travel
   in the non-executable artefact, and can never become test content — which satisfies
   "unverified always equals non-executable" in the safe direction. What is missing is the
   other half: there is no surface that *answers* a scaffold and verifies its
   `expect_failures` behaviour before promoting it. A TODO can be answered and promoted; a
   scaffold cannot. This is the largest gap in the slice.
2. **Assertion provenance is decided at file granularity.** The registry records a file's
   content digest, so "generated-unmodified" is a claim about the file rather than about each
   assertion in it. Editing one assertion reclassifies the whole file as
   `generated-edited` — the conservative direction, since it makes more assertions eligible
   for a *report* and none eligible for a write that does not exist. A per-assertion registry
   would need per-assertion digests written at generation time.
3. **The staged run's verdict cache is scoped to the staging root.** The M2 disposition asks
   for staged bytes to enter the cache identity so that no verdict is reused across changed
   staged content. What is implemented satisfies the safety property by construction — the
   cache is disabled for staged rounds, so nothing is ever reused — and forgoes the speed a
   staged-bytes key dimension would have bought. The property the disposition protects holds;
   the optimisation it implies does not exist.

## 8. Open questions for M5

- Does scaffold promotion belong to characterise at all, or to `suggest`, which already owns
  a verify-then-write protocol? The two write protocols are now adjacent and differ.
- The `moved` refusal's measured cost. If the next review reverses it, the collector is
  small: `moved` names two resource addresses and carries no provider and no effect.
- Validation mining fired zero times over the corpus. Is the rung worth its code, or should
  the constraint go straight to the reader as a judgement point?
- Per-assertion provenance would make `curate` sharper and is a prerequisite for any future
  `curate --apply`.
