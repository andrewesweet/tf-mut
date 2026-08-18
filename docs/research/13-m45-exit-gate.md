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
| **M4.5 offline** | **`just gate-m45`** | 65 named cases, audited by name |
| M4.5-0 measurement | `just measure-synthesis` | network-gated, publishes its own decision |

The M4 gate's check-block floor case is retired: the check block's file is now *read*, so a
name asserting that the floor stays down for it would be a lie.
`TestAMovedBlockInJSONIsReadRatherThanRefused` takes its place in the M4 gate — the same construct class, and a claim about *reading* rather than about the
floor: the block is in the schema, the file is read, and the construct contributes nothing
because there is nothing in it to contribute. A floor standing in for a reading nobody made is
the shape issue #70 was about, and the JSON half of that issue is closed by reading rather
than by refusing. `TestAnImportBlockInJSONIsRefusedByName` holds the refusal for the construct
that earns one.

(An earlier draft of this document and of the pull request named
`TestAMovedBlockInJSONRetainsTheFloor` here. No such test was ever written — the case was
renamed to `…IsRefusedByName` when its claim strengthened, and the name in the prose was not.
The gate-honesty pair cannot catch that: it checks that every name in the recipe resolves to a
test, never that every name in a *document* does.)

## 2. Platform facts measured in this slice, not assumed

Standing rule 3 says never to trust a plausible reading of Terraform behaviour. Six readings
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
4. **An aliased `mock_provider` satisfies a `configuration_aliases` requirement.** A module
   that names its caller's provider configurations and declares no `provider` block is
   therefore characterisable, which is what makes collecting those declarations worth doing
   rather than refusing the module.
5. **`expect_failures = [var.<name>]` passes with a violating input and fails with a
   conforming one.** That asymmetry is what makes scaffold promotion a verification rather
   than a rubber stamp: an answer that does not produce the failure produces a failing run
   block, and the scaffold stays non-executable.
6. **Terraform refuses an assert whose condition references nothing from the configuration**
   (`The condition expression must refer to at least one object from elsewhere in the
   configuration`). This shaped the curate fixture: an assertion with a genuinely empty kill
   set has to read *something*, so the fixture's assertion reads the test's own input rather
   than anything the module produces.

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

- **Validation mining fired zero times across the whole corpus, and it was reached exactly
  four times.** 605 of 609 resolved inputs came from the module's own default, so the
  preference order short-circuits before mining for all but four. Mining works — it returns
  `"bronze"` for `contains(["bronze","silver","gold"], var.tier)` — and public modules simply
  default almost everything. The design's unquantified caveat is confirmed twice over. Mining
  stays; no product claim may rest on it.
- **Refusing `moved` cost one corpus module in ten — and the cost was larger than that
  sentence says.** `terraform-aws-modules/eks` v20.8.5 declares `moved` blocks, and the
  refusal lives in `parseModule`, which every command shares: the module was not merely
  uncharacterisable, *no `tf-mut` command ran on it at all*, with no opt-in to proceed and no
  such regression in M1–M4. The fourth adversarial review blocked on this and it is now
  repaired: `moved` is read and contributes nothing, `import` keeps the refusal. See §12.

## 5. Measurements this slice produced about its own behaviour

- **The until-dry loop converges in one round on every fixture tried, with zero new pins, and
  the reason is structural rather than incidental.** One rendering contract bounds both ends
  of the loop: the harvest pins through `suggest.Express`, and the survivor-suggestion path
  generates through `suggest.Express`. A value the harvest could not express is a value the
  suggestion engine cannot express either, so at a fixed rung there is nothing for a second
  pass to add. Measured on a `null_resource` whose `triggers.env` survives every mutant: the
  mutation loop diagnoses it `no-assertion` and names the fix, and both ends of the
  characterisation loop refuse it for the same reason — a nested map value has no dotted
  spelling the payload path can tell apart from an object attribute. The loop's remaining
  value is in modules whose scenarios leave a branch unpinned, and in deltas whose
  *comparison* is expressible where the harvested *value* was not, such as the
  `length(x) == 0` form. `TestUntilDryConvergesWithoutWritingAByte` proves the loop runs against the
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
| `import` refused in both readers, no opt-in overriding it | `TestAnImportBlockIsRefusedInHCL`, `TestAnImportBlockInJSONIsRefusedByName` |
| `moved` read rather than refused, in both readers | `TestAMovedBlockRunsLikeAnyOtherModule`, `TestAMovedBlockInJSONIsReadRatherThanRefused` |
| the effective staged suite is what the gates judge | `TestAnUntestedAliasedProviderModuleCharacterisesWithNoOptIn` |
| the gates decided before any Terraform execution | `TestNoTerraformRunPrecedesAStagedGateRefusal` |
| a mock per provider *configuration* | `TestAMissingAliasMockRefusesBeforeExecution` |
| effects gate unchanged and unfooled | `checkStagedSafety`, exercised by the provisioner fixtures |
| distinct `state_key` per scenario | `TestScenariosCarryDistinctStateKeys` |
| scenario-reorder invariance | `TestScenarioPinsAreInvariantUnderFileOrder` |
| input synthesis in the preference order | `TestTodosListsTheOpenJudgementPointsWithTheirEvidence`, the corpus measurement |
| TODO material is a non-executable artefact class | `TestAnUnsynthesizableInputBecomesANonExecutableArtefact` |
| `todos`, `--answer`, `--resume`, promotion | `TestAnAnsweredTodoIsPromotedAndTheSuiteIsGreen`, `TestTheTodoSurfacesAreWiredInBothArgumentOrders` |
| a refuted answer is rejected, not fatal | `TestARefutedAnswerIsRejectedRatherThanAnOperationalFailure` |
| the mined rung fires when it is reached | `TestAMinedValidationResolvesAnInputWithNoDefault` |
| a sensitive answer verifies and stays withheld | `TestASensitiveAnswerIsVerifiedAndStillWithheld` |
| a grown closure at the probe | `TestANewClosureFileAtTheProbeYieldsZeroWrites` |
| a partial commit reports what it wrote | `TestAPartialCommitReportsWhatItWrote` |
| double-run volatility exclusion | `skipped-volatile` in `TestTheConfiguredRungPinsOnlyWhatTheConfigurationDetermined` |
| pinning through the M4 renderer and sensitivity machinery | `TestASensitiveValueReachesNoGeneratedArtefact` |
| redaction from the first failed attempt onwards | `TestASecretInAFailedAttemptReachesNoArtefact` |
| the zero-output contract, both halves | `TestAZeroOutputModuleEscalatesAndSaysSo`, `TestARungThatPinsNothingIsNeverComplete` |
| the write protocol and the input-closure digest | `TestAWrittenSuiteIsGreenAndRegistered`, `TestASecondWriteIsRefusedAsACollision`, `TestForceReplacesOnlyUnmodifiedGeneratedFiles`, `TestAClosureChangeAtTheProbeYieldsZeroWrites` |
| shell and file contract; post-path flags refused by name | `TestCharacteriseIsWiredThroughTheCommandLine`, `TestCharacteriseRefusesArgumentsAfterTheModulePath`, `TestTodosRefusesArgumentsAfterTheModulePath`, `TestCurateRefusesArgumentsAfterTheModulePath` |
| `curate` reachable through the command line | `TestCurateIsWiredThroughTheCommandLine` |
| the staged suite; converges without `--write` | `TestUntilDryConvergesWithoutWritingAByte` |
| `--until-dry` ladder-respecting | `TestUntilDryRespectsTheGranularityLadder` |
| `curate` refuses partial populations at configuration time | `TestCurateRefusesAPartialPopulationAtConfigurationTime` |
| `curate` refuses an unobserved population | `checkPopulationObserved`, exercised through the same case set |
| eligibility by provenance; report-only | `TestCurateReportsAnEmptyKillSetWithItsEvidence`, `TestCurateWritesNothing` |
| kill-set participation measured | `TestOneMutantFailingTwoAssertionsAttributesBoth` |
| `expect_failures` scaffolds non-executable | `TestUnassertableConstructsBecomeNonExecutableScaffolds` |
| scaffold promotion is earned by verification | `TestAnAnsweredScaffoldIsVerifiedBeforeItIsPromoted` |
| the final pin set is verified before any write | `TestTheFinalPinSetIsVerifiedBeforeAnyWrite` |
| curate spares its own untouched generated work | `TestCurateDrawsNoConclusionAboutItsOwnGeneratedAssertions` |
| a partial skill install reports what landed | `TestAPartialSkillInstallReportsWhatLanded` |
| every test a document names exists | `TestEveryTestNameADocumentClaimsExists` |
| the characterisation skill under the install protocol | `internal/skill` suite, extended to both skills |
| the falsifiable walkthrough | `TestTheInstalledSkillsWalkthroughExecutes`, `TestASeededWrongFlagInTheSkillTurnsTheGateRed` |
| report-2.3.0 validated on real reports | `TestARealCharacterisationReportValidatesAgainstThePublishedSchema` |

## 7. Built narrower than the spec's words — say so here

Two places. Neither is hidden behind a passing test. (A third — scaffold promotion — was
listed here and is now built; see §8.)

1. **Assertion provenance is decided at file granularity.** The registry records a file's
   content digest, so "generated-unmodified" is a claim about the file rather than about each
   assertion in it. Editing one assertion reclassifies the whole file as
   `generated-edited` — the conservative direction, since it makes more assertions eligible
   for a *report* and none eligible for a write that does not exist. A per-assertion registry
   would need per-assertion digests written at generation time, and is a prerequisite for any
   future `curate --apply`.
2. **The staged run's verdict cache is disabled rather than keyed on staged bytes.** The M2
   disposition asks for staged bytes to enter the cache identity so that no verdict is reused
   across changed staged content. Note where the staged bytes already are: a staged round
   points the whole pipeline at the staging root, so the sources the cache key hashes *are*
   the staged bytes, and a cache enabled there would be keyed correctly without further work.
   It is disabled because its directory is discarded with the round, which satisfies the
   safety property by construction — nothing is ever reused — and forgoes only the speed.

## 8. What the two-axis review changed

The change was reviewed along both axes before it landed — the first of four rounds — and
eight findings were repaired rather than argued with. Four are worth naming because each was a contract the tests as
written could not have caught:

- **The gates ran after `terraform init`.** `warmUp` preceded `checkStagedSafety`, so a
  refusal cost a provider download and a schema read — and the acceptance pair asserted only
  the error text, never that nothing had executed. The gate is now decided from discovery
  alone, before the work root exists, and `TestNoTerraformRunPrecedesAStagedGateRefusal`
  asserts the invocation log the way the JSON floor's own case does.
- **The write protocol's source leg was frozen.** `InputClosureDigest` hashed the source map
  discovery captured once, so the exact defect M1 names — "sources can change after harvest
  while the output stays identical" — was undetectable. Module sources are now re-read from
  disk at every probe, and `TestAClosureChangeAtTheProbeYieldsZeroWrites` stages the race.
- **A refuted answer was an operational failure.** `TodoRejected` was declared and
  unreachable: a wrong answer aborted with a red scaffold instead of coming back as an
  attributed finding. That inverted the safety property `agent-integration.md` §2.4 rests on.
  A refuted answer is now rejected with its diagnostic, the artefact is rewritten, and the
  exit code says work is outstanding.
- **`curate` accepted two partial populations it should have refused.** A tier selection
  narrows the operator population and was unchecked, and a population with timeouts or
  execution errors leaves mutants *unobserved* — which is exactly when an assertion looks
  like it senses nothing. Both now refuse, the second reusing the gate table's
  unobserved-versus-absent distinction rather than restating it.

Also repaired: the never-write contract's exception count and the registry's absence from any
document; a generated file's header recording Terraform's version where it claimed the tool's;
`curate`'s missing command-level cases; four separate identifier derivations collapsed into
`characterise.Identify`; and the middle rung of the preference order, which the corpus
measurement showed fires rarely and which nothing exercised until
`TestAMinedValidationResolvesAnInputWithNoDefault`.

## 9. What the first adversarial review changed

A second adversarial review, against issue #71 and the recorded dispositions, found fourteen
release-contract failures under a fully green M4.5 gate. All fourteen were repaired. Six are
worth naming, because each was a contract no passing case could have caught:

- **`--until-dry` never tested what it generated.** The staged files were built once, before
  the loop, so round N+1 executed round N-1's suite, treated round N's assertions as merely
  *known*, and could declare the run dry without ever running them. The suite is now
  re-rendered from the pins as they stand at the start of every round — and re-rendered
  rather than replayed from the report, because the report's view of a generated file is
  redacted and a suite staged from it would plan a redaction marker.
- **A sensitive answer could never be verified.** One string served as both the reported
  assignment and the planned one, so a sensitive variable's run block was rendered
  `token = (sensitive value withheld)` and every sensitive answer came back as unbalanced
  parentheses. The two views are now separate by construction: the scaffold carries the
  executable assignments, the report carries the redacted ones, and the file digest is the
  written bytes'.
- **JSON `moved` and `import` were unread rather than refused.** Omitting them from the schema
  only lowered the safety floor, and a floor is one opt-in away from being lifted: granting
  both existing opt-ins let a run proceed with neither construct represented anywhere. They
  are now collected and refused by name, and the refusal aborts discovery rather than
  lowering a gate — asserted with both opt-ins granted.
- **The write probe could not see a closure that had *grown*.** Re-reading the path list
  discovery captured misses the whole class of change that matters most: a `.tf` or
  `.tftest.hcl` added since. The closure is now re-*discovered* at every probe — and the
  commit's own target set is excluded from it, without which writing the first generated file
  would change the closure and refuse the second.
- **`length(...)` was read as a counts-rung marker.** The suggestion engine renders a
  configured collection attribute the same way, so `--pin counts` admitted configured-value
  assertions. Classification now reads the addressed subject: a `length` over a bare resource
  address is a count, a `length` over an attribute of one is that attribute's value.
- **A "flip" that flipped nothing.** Returning the compared literal is only an opposite case
  when the base does not already equal it; for `var.env == "prod"` with a default of `"prod"`
  the generated flip set `"prod"` and exercised the same branch.

Also repaired: the counts rung now pins `for_each` *keys* as well as counts, because moving
from `{a}` to `{b}` preserves the count and changes exactly the thing the rung is named for; an
incomplete characterisation no longer exits 0; a partial commit returns its report rather than
only an error; `todos` is dispatched before the version gate, because the shipped skill
promises a cheap local inspection that runs no Terraform; a JSON-declared local no longer
passes for an output and suppresses the zero-output escalation; TODO identity includes the
normalised constraint and its range, so two constraints on one variable stay distinct and a
stale `--answer` cannot re-arm; and pins are deduplicated per scenario, so two scenarios
needing the same rendered condition both keep theirs.

**Scaffold promotion is now built.** A scaffold is answered with the inputs that make the
construct fail — `--answer scf-<id>='{ size = 0 }'` — and the tool renders the
`expect_failures` run block, executes it, and promotes only if Terraform agrees the failure
happened. An answer that does not produce the failure leaves the scaffold non-executable and
says why. Measured first, as the rule requires: on v1.15.8 `expect_failures = [var.size]`
passes with a violating input and *fails* with a conforming one, which is what makes the
verification worth running.

## 10. What the second adversarial review changed

Ten findings against `cf81f4d`, all acted on. The three criticals share one shape, and it is
the shape this branch had already named as its lesson — which is the finding worth carrying
forward rather than any one repair.

**All three were properties nobody asserted, behind tests that asserted something else.**

- `TestASeededWrongFlagInTheSkillTurnsTheGateRed` appended the module path to a transcript line
  that already ended in `.`, so the CLI refused two positional arguments and returned exit 2
  whatever the seed did. Neutering the seed to a no-op left it green in 0.00s. The one test
  that made the end-of-MVP walkthrough falsifiable was itself unfalsifiable. It now runs the
  transcript unseeded (every command must succeed) and then seeded (a refusal must *name* the
  seeded flag), and the seed goes into the fenced block rather than the file's first match,
  because the prose names `--until-dry` several times before the transcript does. Both
  directions verified.
- `unmockedConfigurations` compared `Configurations(configuration)` against a `planned` set
  that *was* `Configurations(configuration)`. No input could separate them; the gate was
  unreachable except through its own seam, and both gate tests set the seam. The mocked set is
  now read out of the **rendered** mock blocks, so the gate checks the renderer against the
  plan.
- `crossScenarioFindings` took the provenance map and used it only to label output, never to
  filter — so curate reported the tool's own untouched generated assertions as redundant, the
  one direction that does damage.

Beyond the criticals: a bounded until-dry exit could write pins no green run covered (the
final set is verified now, asserted by seeding a pin nothing could have harvested); a partial
`skill install` reported nothing; `--generated-functions` escaped curate's population posture;
and the schema now states which bytes `generated_file.digest` covers, because for a sensitive
scenario it is deliberately not the bytes in `content`.

Two findings were about documents rather than code, and one of them changed the gates. The M4
gate lost a case with no replacement, and two documents named
`TestAMovedBlockInJSONRetainsTheFloor` — the pre-rename name of a case whose claim
strengthened, and a test that was never written. `TestEveryTestNameADocumentClaimsExists` now
closes the hole that let it survive: the gate-honesty pair checks recipe-to-test and never
document-to-test, so a document could cite evidence that did not exist and nothing would
notice. A claim about evidence is worth what the evidence is worth.

One defect was found while fixing another. `sandbox.Materialise` documented its target as
"must not exist" and tolerated one that did; materialising twice into one directory produced a
tree resembling neither run, and presented as "the module has no output named tier". The
precondition is enforced rather than described now.

## 11. What the third adversarial review changed

Six findings, every one reproduced before it was repaired, and two of them were writes
escaping their bounds. The theme is narrower than the previous rounds' and worth naming for
it: **every one lived at a boundary that something else had already crossed correctly.**

- **A staged path could write outside the sandbox.** `--test-directory` reaches the keys of
  the sandbox overlay, and `filepath.Join` *cleans* a `..` rather than refusing it, so
  `characterise --test-directory ../../../tmp/x` created a file under `/tmp` — with no
  `--write` given at all. The never-write contract, broken by a relative path. Every staged
  and mutated path is now resolved and confined.
- **A scaffold answer could inject configuration through a key.** The answer grammar was
  constrained on the value side and not on the name side, so
  `{ "size = 0\n  injected" = 1 }` rendered two assignments into a file the staged safety
  check had already approved *because* answers were constants.
- **`configuration_aliases` were invisible.** A reusable module names its caller's provider
  configurations there and has no `provider` block at all, so the alias reached neither the
  mocks nor the gate. The parse detail is worth recording: inside an object-cons value
  `null.primary` is a *relative* traversal, not a scoped one, so a type switch on the scoped
  form matched nothing and reported no aliases rather than failing.
- **The write protocol's probes ran before the rename window rather than inside it.**
  Creating, writing, closing and chmodding a temporary file is real duration. The M4 apply
  protocol had this right already — its `recheck` sits between the chmod and the rename — and
  the characterisation write, written later, regressed it.
- **The provenance registry bypassed the collision protocol it enforces for everything else**,
  and a registry that failed to store discarded a report after every generated file had
  landed.

Two of the six were regressions of patterns this repository already had — the pre-rename
check, and the partial-state report — which is the same shape as the previous round's skill
installer. **A protocol that exists in one place is not a protocol the next writer inherits.**

## 12. Open questions for M5

- Scaffold promotion and `suggest --apply` are now two verify-then-write protocols side by
  side, and they differ. Should they be one?
- The `moved` refusal's measured cost. If the next review reverses it, the collector is
  small: `moved` names two resource addresses and carries no provider and no effect.
- Validation mining fired zero times over the corpus. Is the rung worth its code, or should
  the constraint go straight to the reader as a judgement point?
- Per-assertion provenance would make `curate` sharper and is a prerequisite for any future
  `curate --apply`.
- Three write protocols now exist side by side — `suggest --apply`, the characterisation
  commit and `skill install` — and each has independently grown a pre-rename check and a
  partial-state report, twice by regression review. One shared protocol would be cheaper than
  a fourth rediscovery.
