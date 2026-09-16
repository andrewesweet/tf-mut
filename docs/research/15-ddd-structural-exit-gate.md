# DDD structural programme exit gate — what making the invariants structural measured, decided and deferred

Companion to the adversarial review in [issue #85](https://github.com/andrewesweet/tf-mut/issues/85),
its implementation specification in [issue #86](https://github.com/andrewesweet/tf-mut/issues/86)
and the four decision records under `docs/adr/`. Terraform v1.15.8, Go 1.26.6, 16 September
2026. The review's fixed point is `a52fbe9`; the programme's last landing is `fd5a2d3`
(pull request #150, 2026-09-16). Thirty of the programme's thirty-one tickets are closed;
this document is the thirty-first.

This document takes the number after `14-expression-consumer-measurement.md`, which took the
number issue #117 had reserved for it.

The rule this document is written under: every count, decision and deferral below traces to
a pull request, a decision record, an issue comment or a test in the tree. Where the record
is silent, the document says so rather than supplying a plausible number. Where the
programme deviated from a ticket's words, the deviation is recorded here, not the ticket.

## 1. The gates, and what runs them

| Gate | Recipe | At `a52fbe9` (#85) | At `fd5a2d3` | Recipe change |
| --- | --- | --- | --- | --- |
| M2a honesty | `just gate` | 21 | 21 | none |
| M3 offline | `just gate-m3` | 83 | 83 | none |
| M4 offline | `just gate-m4` | 123 | 124 | one name added by #116 |
| M4.5 offline | `just gate-m45` | 128 passed, one documented CLI subcase skipped | 132, the same one skipped | one name added by #116 |

The exit counts are from running the four recipes at `fd5a2d3` with the provider mirror
installed, as `gotestsum` reports them (the M4.5 figure includes its skip). The entry counts
are #85's; the record disagrees with itself by one case there — #90's pull request recorded
127 passed and one skipped against the unchanged recipe — and this document does not
resolve which count was taken how. The one addition to both recipes is
`TestAJSONDeclaredDirectiveConditionReachesTheScaffold`, the case that gates the programme's
one authorised behaviour change (§6). The remaining three M4.5 cases are subtest growth,
not new names: between the two points `TestAClosureChangeInsideTheRenameWindowIsCaught`
became a three-case `t.Run` table (`change only`, `file only`, `file then change`), and
`gotestsum` counts each subtest as a case. Every other `-run` pattern is byte-identical to the
fixed point, and `TestTheHonestyGateNamesOnlyTestsThatExist`,
`TestTheM3GateNamesOnlyTestsThatExist`, `TestTheM4GateNamesOnlyTestsThatExist` and
`TestTheM45GateNamesOnlyTestsThatExist` passed on every landing without an edit to their
name lists. The two cases that moved — `TestCurateRefusesAPartialPopulationAtConfigurationTime`
and `TestUntilDryRefusesANarrowedPopulation`, relocated from the engine seam to
`cmd/tf-mut/cli_test.go` by #97 because the fields they staged no longer exist — kept their
names, and `gate-m45` already listed `./cmd/tf-mut/`.

Between the two points the test surface grew from 395 to 415 top-level test functions and
from 75 to 81 engine fixtures, counted the same way at both points (`^func Test|Fuzz` over
`internal` and `cmd`; directories under `internal/engine/testdata`). No test function was
removed. The twenty additions are listed in §4; the six fixtures are #93's
`untested-lifecycle-persistence` and #116's five.

The programme's own contract — "no new behavioural test is written for increments 1 to 3,
and every existing test passes unchanged in name and in assertion" (#86, Testing Decisions)
— was held to on every landing by the four gate recipes plus `just ci`, and by the
base-versus-target binary comparisons listed in §6.3.

## 2. What was measured

### 2.1 The expression boundary: a compiler-derived consumer classification

Published in full in `docs/research/14-expression-consumer-measurement.md` (#111, pull
request #132). Standing rule 4 applied to a representation choice: before
`discovery.Attribute.Expr` and `discovery.Validation.Condition` moved from
`hclsyntax.Expression` to `hcl.Expression`, three scratch passes let the compiler enumerate
every consumer — a retype, a refinement that narrowed each bridged helper's own signature to
the interface, and a census that renamed both fields and iterated `go vet` to convergence so
that type assertions, which a method-set check cannot see, were counted too.

**17 access sites. 13 satisfied by `hcl.Expression` — 8 read-consumers and 5 producer or
construction sites — and 4 genuinely requiring concrete `hclsyntax` nodes. None served by
neither.**

| Package | Sites | Satisfied | Genuine native |
| --- | --- | --- | --- |
| `internal/discovery` | 6 | 5 | 1 (`referencesOf`, the closure walk) |
| `internal/characterise` | 7 | 5 | 2 (`mineExpression`, `synthesiseType`) |
| `internal/mutation` | 2 | 2 | 0 |
| `internal/engine` | 2 | 1 | 1 (`mutatedMultiplicity`, the static `NoCoverage` evaluator's admission boundary) |
| every other package | 0 | — | — |

Two things the measurement established that the tickets had assumed otherwise. The mutation
catalogue's token rewriting does not flow through the discovery boundary at all — its
operators work on `*hclsyntax.Attribute` values the generator re-parses from source itself —
so #112 shrank to narrowing one helper, `literalBool`, to the interface (pull request #137).
And the refinement pass surfaced one site (`mutatedMultiplicity`'s second return) that the
retype pass's bridge had masked, which is the reason the refinement step exists.

The reopen condition ADR-0004 states for a custom expression model — "a measured consumer that
requires one" — did not fire. `go vet` over every `_test.go` reported zero test-file failures:
no test reads either field.

**The estimate the measurement replaced.** #86 sized the boundary at "around 115
`hclsyntax.` references in non-test production code". The record does not state how that
number was counted, and it does not reproduce: a plain `grep -c 'hclsyntax\.'` over non-test
Go under `internal` and `cmd` at `a52fbe9` gives 293 lines (311 occurrences), of which
`internal/mutation` alone holds 122 and `internal/discovery` 97; the four packages outside
those two hold 74. The estimate shaped the decision to measure; the 17-site census is the
number the migration was cut against.

### 2.2 The seam-control count correction

Issue #83 said twenty-four `Seed*` fields sat on the exported `engine.Config`. The review
re-verified the fixed point and found **twelve `Seed*` fields plus two `Disable*` fields,
fourteen in all** (#85 DDD-3; #85's closing comment; #86's problem statement; #83's closing
comment on pull request #126). The defect stood; the count was stale. The fourteen, from
`internal/engine/engine.go` at `a52fbe9`:

`DisableJSONReading`, `DisableStaticShortcuts`, `SeedClosureAfter`, `SeedClosureChange`,
`SeedClosureFile`, `SeedFinalPinDefect`, `SeedInitialPinDefect`, `SeedMissingMock`,
`SeedNoEscalation`, `SeedRegistryFailure`, `SeedRenameWindowChange`, `SeedSharedFileOrder`,
`SeedSuggestionDefect`, `SeedUntilDryRounds`.

They left the exported surface in three batches cut by driving file rather than as one
fourteen-field change — the re-cut #86's route assessment names as chosen to keep every batch
independently green:

| Batch | Controls | Now beside | Pull request |
| --- | --- | --- | --- |
| #92 | the five characterisation-write controls | `internal/engine/characterisewrite.go` | #123 |
| #93 | the five characterisation-planning controls | `internal/engine/characterise.go` | #124 |
| #94 | `SeedSuggestionDefect`, `SeedUntilDryRounds`, `DisableStaticShortcuts`, `DisableJSONReading` | `suggest.go`, `untildry.go`, `engine.go` (both `Disable*`) | #125 |

The route assessment counted "six driving files"; the hooks landed in five, `engine.go`
holding both `Disable*` hooks. Test assignments at the fixed point: 26 across 9 test files
by a plain count of `.Seed* =` and `.Disable* =`; #86 said "roughly twenty-eight across nine".

Each migrated control was shown to still turn its gate red before landing — the discipline #86
required because "a skip and a pass are the same colour" — by inverting or neutralising the
hook and observing the named failure, then restoring the source exactly. The witnesses, from
the three pull requests: `TestAClosureChangeAtTheProbeYieldsZeroWrites`,
`TestANewClosureFileAtTheProbeYieldsZeroWrites`, `TestAPartialCommitReportsWhatItWrote`,
`TestAClosureChangeInsideTheRenameWindowIsCaught`,
`TestARegistryFailureReportsThePartialState` (#92);
`TestAMissingAliasMockRefusesBeforeExecution`, `TestTheFinalPinSetIsVerifiedBeforeAnyWrite`,
`TestASeededInitialPinDefectIsRejectedBeforeAnythingIsWritten`,
`TestARungThatPinsNothingIsNeverComplete`, `TestScenarioPinsAreInvariantUnderFileOrder`
(#93); `TestASeededWrongValueIsRefutedThroughTheBaselineLeg`,
`TestUntilDryConvergesWithoutWritingAByte`, `TestStaticUnobservableEqualsTheExecutedVerdict`,
`TestUnreadJSONDisablesEveryStaticShortcut` (#94). Two of the fourteen — `SeedClosureChange`
and `SeedClosureFile` — write into the caller's tree; both are now unreachable from any
exported symbol, which is the acceptance criterion #83 stated.

One witness needed more than a disabled hook. `TestScenarioPinsAreInvariantUnderFileOrder`
cannot go red by disabling a valid permutation, because every valid layout has equal public
results by design; #93 collapsed the distinct state keys inside the hook-driven shared-file
path instead, and the test reported `the pins depend on file order` — proving the hook reaches
the fault-bearing path. #93 also moved that case onto an offline lifecycle-persistent fixture
comparing scenario/address/expression tuples, because an order-independent set of values alone
could hide state leaking between scenarios.

`TestExportedEngineTypesDeclareNoSeamControls` (#95, pull request #126) now reads the engine
package's production sources and rejects a `Seed*` or `Disable*` field on any exported type,
including pointer and generic embeddings; #100 extended it to the six request types. Its
deliberate reintroduction proofs — a compile-valid `Config.SeedAuditProof`, then four
embedded forms — failed by naming the offending fields.

### 2.3 The call-site counts that shaped the migration batches

The route assessment for #86 rested on "measured counts ... rather than on ticket wording".
Those counts, with what the tree at `a52fbe9` shows when recounted:

| Count | Record | Recount at `a52fbe9` | Shaped |
| --- | --- | --- | --- |
| `baseConfig` calls in engine tests | 166 across 28 test files, funnelling through about ten helpers (#99) | 166 across 28 files | #99 as the largest, most mechanical batch: retype the helpers, sweep the rest |
| `Preview`/`Suggest` mode-boolean assignments | 20 `Preview` + 1 `Suggest` across 13 engine test files (pull request #128) | 21 `.Preview = true`, 1 `.Suggest = true` | #98 |
| characterise-family mode-boolean assignments | 6 `Curate`, 9 `UntilDry`, 2 `Todos`, 1 `Characterise` (#97) | 6, 9, 2, 1 | #97, and its two relocated gate cases |
| verdict-construction sites | 21 across five files: 12 survivor and unassertable sites in `oracle.go` (#101), 9 terminal sites — `conditional.go` 1, `execute.go` 5, `policy.go` 2, `engine.go` 1 (#102) | not recounted | the #101/#102 split of the Oracle context |
| seam controls | 14 mapped to six driving files (route assessment) | 14; five files | the #92/#93/#94 batches (§2.2) |
| boundary access sites | 17 (#111) | reproduced live by pull request #132 | #112, #113, #114 as three migrate batches before the #115 contract |

The one-site difference on the `Preview` count is between the pull request's own statement
and a plain grep; the record does not say which site accounts for it, and it is not
material — #100 deleted the field, so no assignment survives either way.

### 2.4 The cost the ticket shape itself carried

The programme's second retrospective, quoted in pull request #139, measured three of five
batch-two tickets losing a merge-conflict repair and revalidation — about $21 and 40 minutes
per batch — because every ticket rewrote the same `CONTEXT.md` paragraph enumerating which
tickets had landed, and edited adjacent rows of the `AGENTS.md` implementation map. #139
replaced the enumeration with a pointer to issue #86 as the programme's authoritative status.
That is the one process measurement the programme produced about itself.

## 3. What was decided

Each decision names its record. The records were written first (#88, pull request #120,
2026-09-07) as target decisions, and each carries an evidence section that grew as tickets
landed.

### 3.1 Contexts are package groups, and the dependency rule is a lint rule — ADR-0001

Six contexts within one Go module, named in `CONTEXT.md` (#87, pull request #119) with the
vocabulary each owns: Oracle / Verdict (core), Mutation Catalogue, Suggestion and
Characterisation (supporting), Terraform Boundary and Publication (generic, anti-corruption).
`internal/engine` is the synchronous application layer; `cmd/tf-mut` an adapter;
`internal/config`, `internal/buildinfo` and `internal/buildchain` infrastructure outside the
map. One root `CONTEXT.md` rather than a context map plus six files, because `AGENTS.md`
declares the repository single-context.

The direction is a `depguard` rule, `context-boundary` in `.golangci.yml`, denying
`github.com/andrewesweet/tf-mut/internal/report` to the context packages. It entered with the
five packages that already complied (#89, pull request #122: `fingerprint`, `mutation`,
`discovery`, `tfexec`, `sandbox`), gained `internal/oracle` when that package was created
(#101) and closed over `internal/suggest` and `internal/characterise` last (#109), test files
included and with no `!$test` escape hatch — which is why `internal/suggest/adapters_test.go`
(AGENTS.md recorded exception 4) now asserts the context-owned `suggest.SkipReason` rather
than `report.SuggestionStatus`. Pull request #122 proved the mechanism by adding a temporary
import from `fingerprint` to `internal/report` and observing `just lint-go` fail with the
`context-boundary` diagnostic.

A hand-written architecture test was rejected because the configured linter already answers
the question; a context map without enforcement was rejected because it records intent
without failing when intent is broken. `internal/skill` is grouped under Publication but,
since #91, writes through `internal/sandbox.WriteFreshCheckedMode`; `CONTEXT.md` and
`docs/reviews/2026-09-07-issue-87-context-reconciliation.md` record that as the one
Publication-to-Terraform-Boundary exception, not a dependency from the report leaf.

### 3.2 Invariants live in closed constructors over unexported fields — ADR-0002

Domain contexts own values that exist only where a constructor proved the combination legal;
the application layer projects them onto the unchanged `report` DTOs. A type per status value,
schema-first invariants and post-construction validation functions were each rejected. What
landed, context by context:

**Oracle / Verdict** (`internal/oracle`, new). `Outcome` and `Evidence` with unexported fields.
Sixteen constructors: the five survivor constructors whose parameters are their diagnosis's
required evidence subset (`SurvivedIndeterminateUnknowns`, `SurvivedIndeterminateVolatility`,
`SurvivedWeakAssertion`, `SurvivedNoAssertion`, `SurvivedUnasserted`); the nine terminal
constructors #86 named, none accepting a diagnosis (`Killed`, `KilledByError`, `TimedOut`,
`Invalid`, `NoCoverage`, `Ignored`, `Pending`, `StructurallyUnassertable`, `Unobservable`);
and two the tree needed that the specification did not name — `StaticallyUnobservable`, the
empty-cone claim decided from the reference graph rather than a comparison and therefore
carrying no mask, and `SurvivedPhaseOne`, the provisional phase-one state published only when
phase two could not run (#102, pull request #135). The precedence table stays in
`internal/engine/oracle.go` and is, with cache rehydration, the constructors' only caller.
`ComputeMetrics` and `ComputeOperatorErrors` moved with the type they compute over.

The second construction path — the verdict cache — closed under #103 (pull request #138):
`oracle.ParseRecord` rebuilds an `Outcome` from a stored record and returns
`ErrIllegalRecord` for any document the constructors could not have produced: a non-survivor
carrying a diagnosis, an unknown state or diagnosis, an empty finding where one is required, a
finding on a verdict-less state, an `Ignored` record without a suppression, or evidence
outside the subset its state and diagnosis record. An illegal entry is a miss and the mutant
re-executes; the on-disk format and every key dimension are unchanged, shown by the target
binary hitting on entries the base binary wrote under the same key. The base binary replayed
a `Killed` entry carrying a `weak-assertion` diagnosis as a cached verdict; that is the defect
this closed.

**Suggestion** (`internal/suggest`, #105, pull request #134). `Candidate` is the only value
that can be verified or refuted; `Candidate.Verify` demands the digest and both legs,
`Candidate.Refute` a reason and both legs, and `Skipped` takes no patch. The address,
rendering and sensitivity adapters return the closed `SkipReason` rather than a publication
status. Live suggestion output was byte-identical to the base binary on three fixtures across
JSON, terminal and SARIF, and the fixture secret appeared in no artefact.

**Characterisation** (`internal/characterise`; #107 pull request #136, #106 pull request #142,
#108 pull request #146). Judgement points move only through transitions — `OpenTodo`,
`Answer`, `Promote`, `Reject` — with promotion reachable only from an answered point carrying a
`Verification` the engine's verifier built; `Input` and `InputProvenance` (`FromDefault`,
`FromValidation`, `FromType`, `FromAnswer`) written by the preference order alone; `Pinned`
and `PinSkipped` the only spellings of a pin, the skipped one taking no expression;
`Scaffolded` and `Scaffold.Promote` taking the verification as its only route to `promoted`;
`Scenario` identity, name, state key and inputs behind accessors. After #108 the package has
no production import of `internal/report`.

**The population proofs** (`internal/engine/population.go`, #104, pull request #148).
`authoritativePopulation` is constructible only from a classified population with no timeouts
and no execution errors — `checkPopulationObserved`'s former logic, now a constructor — and
`freshPopulation` refines it with no cached verdict. `curate`'s findings and the `--until-dry`
absorb step accept the former; `writeBaseline` accepts only the latter and cannot be reached
another way. The configuration half of the baseline rule — `--since` and `--sample` refused
before any work — stays a pre-flight check on `RunRequest`, because making `--write-baseline`
and `--since` mutually unspellable would need two run request types for one guard.

**The projection is total, and pinned per command.** `TestTheStatusProjectionIsTotalInBothDirections`
(#109, pull request #149) drives every context-owned status — suggestion statuses and skip
reasons, pin, TODO and scaffold statuses, input provenances, oracle diagnoses and evidence
fields — through the projection and holds the result against a mechanical census of both
sides read from the owning packages' constant declarations and the published evidence struct.
The withdrawn `mock-masked` diagnosis and `Evidence.MockResource` are the only named
exemptions. Four seeds were shown to turn it red and were reverted: a context status with no
wire spelling, a wire spelling no context reaches, a published `Evidence` field no constructor
writes, and the projection emitting `MockResource`. The last two came from the pull request's
own review round, which found the first version's census vacuous in one direction.
`internal/engine/blocks_test.go` (#110, pull request #147) pins each request type's
projection to its own report blocks, one test per command (§4).

### 3.3 Cross-context handoffs are synchronous immutable result facts, not events — ADR-0003

Each context returns its own values — `[]oracle.Classified` and `oracle.Metrics`, a
`suggest.Suggestion` concluded through `Verify` or `Refute`, `characterise.Pin`, `Todo`,
`Scaffold` and `Scenario` — and the engine projects them. Nothing is persisted, dispatched,
subscribed to or replayed. An event bus, dispatcher or event store is reconsidered only when
an actual independent asynchronous consumer exists; the condition is written into the record
so that the decision reopens on evidence rather than fashion.

One point the record shows built differently from #86's words. The specification said the
named results would be `PopulationClassified`, `SuggestionVerified`, `SuggestionRefuted` and
`SuiteCharacterised`, "each a plain immutable value returned from the context". No type of
any of those names exists in the tree; `report.SuggestionVerified` and
`report.SuggestionRefuted` are the pre-existing wire-status constants. The handoffs return the
context types themselves, which are immutable and context-owned; a named fact per handoff
was not minted. See §7 for what this means against the rubric.

### 3.4 `discovery` publishes `hcl.Expression`; native syntax has named accessors — ADR-0004

Decided as a target on 2026-09-07 "subject to the consumer measurement required by #111", then
answered in four steps: the accessor and the measurement (#111), three migrate batches
(#112, #113, #114 — pull requests #137, #140, #141), and the contract (#115, pull request
#144), whose diff touched two field types, their doc comments and one `AGENTS.md` row.
Consumers that need only a value read the interface with no accessor call and no unreachable
fail-closed branch; the four genuine consumers reach native syntax through
`discovery.NativeExpression`, which fails closed — `false` for a JSON-syntax expression and
for `nil` — and a site it refuses is not a mutation site, which was already the JSON safety
floor's rule.

**The addendum.** #116 (pull request #150) then stopped the JSON configuration reader
re-rendering declarations into native syntax (§6), and the two characterisation rungs that
genuinely need tokens — the typed rung's type constraint and the mined rung's condition —
re-parse at their own point of use through `discovery.ReparsedNative`, "the second and last
named accessor". It fails closed on any spelling that is not one native expression. ADR-0004
carries this as a dated addendum; #86 and #117's acceptance criteria say "one named native
accessor", and the record now says two. The pull request's review found the first
version had re-parsed inside `internal/characterise` through two private routes, and the fix
rounds collapsed them onto the one named discovery accessor rather than amending the ADR to
sanction a private route — the direction the precedence rule requires. A custom expression
model was not introduced; §8 records the condition that would justify one.

### 3.5 One shared write primitive, three distinct precondition sets — ADR-0004

`internal/engine/apply.go` and `internal/skill/skill.go` each hand-rolled the create-temp,
write, close, chmod, rename sequence that `sandbox.WriteFreshChecked` already implemented and
`characterisewrite.go` already used. #91 (pull request #131) added `WriteFreshCheckedMode`,
the same sequence with the installed mode chosen by the caller, and migrated both. Each
workflow keeps its own preconditions in its own `commit` closure at the rename boundary: the
verified-digest re-check for `suggest --apply`; the input-closure digest and registry
ownership for the characterisation commit; the user-edit and version checks for `skill
install`, which gained `ErrTargetChanged` for a file that changed between decision and write.
No generic write aggregate was introduced, because the three workflows' snapshot, ownership,
collision and partial-state rules are genuinely different. No new test was written; the nine
named apply and skill cases, the commit-window race case and `just gate-m4`/`gate-m45` were
the proof, as #86 required: "if the migration needs a new test to prove it safe, it changed
behaviour and is wrong."

### 3.6 The closed request set, recorded in `AGENTS.md` rather than an ADR

`engine.Run` takes `Request`, an interface closed by an unexported marker method, with six
request types — `RunRequest`, `PreviewRequest`, `SuggestRequest`, `CharacteriseRequest`,
`TodosRequest`, `CurateRequest` — over shared `Common`, `Population` and `Gate` option
values. Expanded alongside `Config` (#96, pull request #127), migrated in three batches
(#97, #98, #99), contracted by unexporting `Config` and deleting the five mode booleans
(#100, pull request #145). `Run` dispatches on a type switch; `TestRunRefusesNilRequests`
holds the nil case. `CurateRequest` carries no `Population`, so `--since`, `--sample`, a tier,
an operator selection or an exclusion have nowhere to land on it. It does carry `NoCache`,
which #86's sketch omitted and #97 added because the flag reaches the engine.

This decision has no ADR. It lives in `AGENTS.md`'s testing-seam section, amended by #100 in
the same change, as the repository's precedence rule requires: the seam keeps its altitude,
its real-Terraform discipline and its external-behaviour-only rule, and gains a closed input
type. The "fixed decision — do not reopen per milestone" wording was written to stop the
altitude being relitigated, not to prevent a deliberate refactor from typing the input.

**The maintainer's ruling on #97** (2026-09-15, "As recommended", carried in pull requests
#143, #145, #146 and #148) constrains the family:
configuration-narrowed populations from `.tf-mut.hcl` stay refused at configuration time for
`curate` and `--until-dry`, exactly as the M4.5 spec review's C5 disposition states; only
flag-based narrowing moved to the CLI parser as `errInapplicableFlag`.
`TestCurateRefusesAConfiguredNarrowing` and `TestUntilDryRefusesAConfiguredNarrowing` hold
the engine-side half.

### 3.7 The documented vocabulary is checked against the binary — DDD-6

#90 (pull request #121) repaired the drift #85 listed and added
`TestTheDocumentedVocabularyMatchesTheBinary`: the README's forward-cone rule and the
whole-payload floor, all seven reporters and the emitted schema version, the five never-write
exceptions by link, `--reporter json` in the agent-integration document, the product-design
opening corrected to the measured inner loop with run-block splitting recorded as dropped,
the fourth synthesis rung marked "specified but not implemented" with a link to #82, and the
historical `mock-masked` prescriptions in `03-hcl2-tooling.md`, `04-harness-spike.md` and
`08-m2-exit-gate.md` annotated as withdrawn with their evidence. The test is a token check by
design: no prose parser. It also guards against the stale `moved` bullet in
`13-m45-exit-gate.md` §14 being reintroduced.

## 4. Contract sweep

The normative behaviours #86 states, and the test that proves each. Behaviours the programme
was forbidden to change are proved by the pre-existing named cases; the new tests are all
structural or document-contract, plus the one behavioural case the JSON change earned.

| Normative behaviour (issue #86) | Proved by |
| --- | --- |
| no context package imports `internal/report`, tests included | the `context-boundary` `depguard` rule in both `just lint-go` builds; the red proof in pull request #122 |
| no exported engine type declares a seam control | `TestExportedEngineTypesDeclareNoSeamControls` |
| a nil request is refused, a pointer request accepted | `TestRunRefusesNilRequests` |
| a flag of one command cannot be spelled on another | `TestACharacterisationFlagIsRefusedByAGradingCommand`, `TestCurateRefusesAPartialPopulationAtConfigurationTime`, `TestUntilDryRefusesANarrowedPopulation` |
| configured narrowing still refused at configuration time (C5) | `TestCurateRefusesAConfiguredNarrowing`, `TestUntilDryRefusesAConfiguredNarrowing` |
| every survivor verdict, message and fix byte-identical | `just gate`, and the base-versus-target comparisons in §6.3 |
| an illegal cached record is a miss | `TestCorruptionIsAMiss`, `TestASecondUnchangedRunIsAllCacheHits`, `TestCacheInvalidationPerKeyDimension`, `TestTheCachedRowOfTheGateTable` |
| metrics and per-operator error rates byte-identical | `TestExitCodesAreDeterministicAcrossTheTable`, the count-lever cases in `just gate-m3` |
| `curate` and `--until-dry` draw only over an authoritative population | `TestCurateRefusesAnUnobservedPopulation`, `TestUntilDryRefusesAnUnobservedPopulation`, `TestCurateDrawsNoConclusionAboutItsOwnGeneratedAssertions` |
| the baseline writer accepts only a fresh population | `TestBaselineWriteIsRefusedOffTheFullPopulation`, `TestBaselineWriteIsRefusedOverAnUnobservedPopulation`, `TestAdoptionThenRegression` |
| a verified suggestion is unspellable without both legs; a skipped one without a patch | `TestVerifiedRequiresBothLegsAndCarriesTheirEvidence`, `TestEverySkippedStatusCarriesNoPatchAndAReason`, `TestEveryOutcomeTableRowIsReachableThroughTheSeam`, `TestAGeneratorLimitIsNeverARefutation` |
| the three adapter matrices decide as before, over `suggest.SkipReason` | `TestTheAddressAdapterMatrix`, `TestTheRenderingContractMatrix`, `TestTheSensitivityPredicateRefusesBeforeAnythingRenders` |
| a skipped pin carries no expression; pins go through the M4 adapters unchanged | `TestASensitiveValueReachesNoGeneratedArtefact`, `TestTheConfiguredRungPinsOnlyWhatTheConfigurationDetermined`, `TestScenarioPinsAreInvariantUnderFileOrder`, `TestBranchExpansionPinsBothSidesOfAConditional`, `TestARungThatPinsNothingIsNeverComplete` |
| an unverified answer cannot appear as promoted content | `TestAnAnsweredTodoIsPromotedAndTheSuiteIsGreen`, `TestARefutedAnswerIsRejectedRatherThanAnOperationalFailure`, `TestASensitiveAnswerIsVerifiedAndStillWithheld` |
| a scaffold is promoted only through verification | `TestAnAnsweredScaffoldIsVerifiedBeforeItIsPromoted`, `TestUnassertableConstructsBecomeNonExecutableScaffolds`, `TestAnUnsynthesizableInputBecomesANonExecutableArtefact`, `TestScenariosCarryDistinctStateKeys`, `TestForEachKeysNeedingEscapesStillRenderAGreenSuite` |
| the status projection is total in both directions | `TestTheStatusProjectionIsTotalInBothDirections` |
| each command's report carries only its own blocks | `TestARunReportCarriesNoSuggestionsNoApplyRecordAndNoCharacterisation`, `TestAPreviewReportCarriesNoGates`, `TestASuggestReportCarriesNoApplyRecordAndNoCharacterisation`, `TestACharacterisationReportCarriesNoGatesNoSuggestionsAndNoApplyRecord`, `TestACurateReportCarriesACharacterisationBlockAndNoSuggestions`, `TestATodosReportCarriesACharacterisationBlockAndNoSuggestions` |
| the published schema stays `2.3.0` on real reports | `TestARealSuggestReportValidatesAgainstThePublishedSchema`, `TestARealCharacterisationReportValidatesAgainstThePublishedSchema`, `TestARealCurateReportValidatesAgainstThePublishedSchema` |
| the validation-function table decides as before through the accessor | `TestTheValidationFunctionTable`, `TestAMinedValidationResolvesAnInputWithNoDefault` |
| the static `NoCoverage` evaluator and the static shortcut decide as before | `TestABodyOnlyMutantOfAnUninstantiatedBlockStaysNoCoverage`, `TestAMutantInsideTheConditionExecutes`, `TestAMutantUpstreamOfTheConditionExecutes`, `TestExcludedCategoriesFailClosedToExecution`, `TestStaticUnobservableEqualsTheExecutedVerdict` |
| every generation site maps into the graph after the boundary moved | `TestEveryGenerationSiteMapsIntoTheGraph` |
| no JSON file is a mutation site; reading JSON changes no population | `TestNoJSONFileIsEverAMutationSite`, `TestAJSONPopulationIsUnchangedByReadingIt`, `TestAJSONDeclaredChildVariableAddsNoMutant`, `TestAJSONDeclaredSensitiveVariableRemovesNoMutant` |
| a JSON declaration is read rather than fail-closed around | `TestAJSONDeclaredDirectiveConditionReachesTheScaffold`, `TestAJSONDeclaredVariableReachesTheScaffold`, `TestAJSONDeclaredValidationIsMinedAsANativeOneIs`, `TestAJSONDeclaredValidationReachesTheTodoVerbatim`, `TestAJSONDeclaredModuleCallJoinsTheClosure` |
| the JSON safety floor holds unchanged | the nine floor cases #116 names, from `TestUnreadJSONFailsTheRealInfrastructureGateClosed` to `TestNoTerraformRunPrecedesAFloorRefusal`, in `just gate-m4` |
| the three write workflows share the primitive and keep their preconditions | `TestACleanApplyWritesAtomicallyAndTheMutantsDie`, `TestAnEditBetweenVerificationAndApplyAbortsWithZeroWrites`, `TestAStaleVerifiedDigestAbortsThePreflightNamingBothDigests`, `TestASymlinkedTargetAbortsBeforeAnyWrite`, `TestAJSONTestFileIsNeverWrittenByApply`, `TestAMultiFileApplyReportsAPartialFailureExplicitly`, `TestApplyIsTheThirdWriteExceptionAndTouchesOnlyItsTargets`, `TestAUserEditSurvivesAReinstallUnlessForced`, `TestAPartialSkillInstallReportsWhatLanded`, `TestAnEditBetweenPreflightAndCommitAbortsTheReplacement` |
| the documented vocabulary matches the binary | `TestTheDocumentedVocabularyMatchesTheBinary`, `TestTheInstalledSkillReferencesOnlyCommandsAndFlagsTheBinaryHas` |
| every test a document names exists | `TestEveryTestNameADocumentClaimsExists` |

The twenty test functions the programme added, none of them behavioural except the five
JSON cases: the six per-command block tests; `TestTheStatusProjectionIsTotalInBothDirections`;
`TestExportedEngineTypesDeclareNoSeamControls`; `TestRunRefusesNilRequests`;
`TestTheDocumentedVocabularyMatchesTheBinary`; the five population refusals
`TestCurateRefusesAConfiguredNarrowing`, `TestUntilDryRefusesAConfiguredNarrowing`,
`TestCurateRefusesAnUnobservedPopulation`, `TestUntilDryRefusesAnUnobservedPopulation` and
`TestBaselineWriteIsRefusedOverAnUnobservedPopulation`; and the five JSON cases. Of these,
the five JSON cases are the behavioural tests #86 allowed; the three unobserved-population
refusals are behavioural where #86 said none would be (§6.2); and the two configured-narrowing
cases keep an engine-side refusal that the relocated gate cases had staged through request
fields which no longer exist.

## 5. Built differently from the ticket's words — say so here

The specification was published as thirty-one tickets whose reasoning "was done in the
spec", and thirty of them landed. In these places the tree differs from the ticket that
produced it. Each was confirmed from the pull request and issue record rather than from a
brief; where the record and the list this document was asked to check disagreed, the record
won.

1. **`Unobservable` and `StructurallyUnassertable` were built in #101, not #102.** #86 and
   #102 list both among the terminal constructors; #101's acceptance criteria name only the
   five survivor constructors. Pull request #133 built the seven, because #101's own criterion
   — "the twelve survivor and unassertable construction sites in `internal/engine/oracle.go`
   build outcomes through the constructors" — includes the sites that produce those two
   states, and its commit message says so: "the two terminal constructors the oracle's own
   unassertable sites need". #102 added the seven remaining named terminal constructors plus
   the two of §3.2.
2. **`PinSkipped` takes five parameters, not two.** #86 and #106 wrote
   `PinSkipped(reason SkipReason, detail string)`. The tree has
   `PinSkipped(scenario, address, rung, reason, detail)`, because a skipped pin still names
   the scenario, address and rung it was skipped at; pull request #142 records this as "a
   documented signature deviation from the ticket's literal `PinSkipped`". The invariant the
   ticket wanted — no expression parameter — holds. `suggest.Skipped` deviates the same way
   for the same reason: `Skipped(mutantID, targetFile, targetRun, reason, detail)` against
   the sketch's `Skipped(reason, detail)`, still with no patch parameter.
3. **#116's second criterion was staged on a validation condition, not a type constraint.**
   The criterion read "a `.tf.json` variable whose type constraint does not re-parse into
   native syntax reaches the scaffold rather than becoming a judgement point". Pull request
   #150's review found the literal case unstageable: no Terraform-legal JSON type constraint
   can fail to re-parse, because Terraform parses the same string through `hclsyntax` itself.
   The honest analogue is a validation condition written as a `%{ if … }` template directive,
   which is what `untested-json-type` stages and
   `TestAJSONDeclaredDirectiveConditionReachesTheScaffold` gates. A type constraint that
   somehow did not re-parse would still fail closed in the typed rung and become a judgement
   point.
4. **The JSON mining limitation was reversed inside #116.** #113 (pull request #140) had
   recorded, as pre-existing and out of its scope, that a JSON-declared variable with no
   default never reached synthesis. Pull request #150's first version relaxed the type
   constraint but left `mineExpression` requiring native syntax, so a `.tf.json`
   `"${contains([...], var.tier)}"` condition reached the scaffold and then became a judgement
   point where the identical native condition mines `"a"` and pins. The review named the
   asymmetry; the fix rounds re-parsed at the mined rung's point of use too, through
   `ReparsedNative`. `TestAJSONDeclaredValidationIsMinedAsANativeOneIs` holds it, and it
   fails on the base commit.
5. **A static `NoCoverage` speed path opened, and is recorded here rather than in the pull
   request's description.** After #116, `resolveVariable` reads the root module through
   `VariableByName`, which now includes JSON-declared variables, so a `count` or `for_each`
   decided solely by a `.tf.json` `default` is classified `NoCoverage` statically where the
   base commit executed it. The population is unchanged — mutation reads `NativeVariables()`
   only — and the static/executed equivalence contract makes this a speed path rather than a
   verdict change, but it is a second observable difference from reading JSON. The review
   asked for one sentence in the pull request description and in this document; the merged
   description does not carry it. This is that sentence.

Also differently: the sixteen oracle constructors against fourteen named (§3.2); the four
named result facts not minted (§3.3); `ReparsedNative` as a second accessor (§3.4);
`CurateRequest.NoCache` (§3.6); and the seam hooks in five files against six (§2.2).

## 6. Behaviour: the one authorised change, and what else the record shows moved

### 6.1 JSON declarations are read rather than re-parsed — #116, pull request #150

The programme's one named behaviour change, stated by #86 as "a deliberate behaviour
improvement" and by #116 as "the only behaviour change in the whole programme, an
improvement rather than a regression". Before it, the JSON configuration reader rendered and
re-parsed every `.tf.json` declaration into native syntax because every downstream reader
demanded the concrete type, and a declaration whose parts did not re-parse was dropped at the
boundary: fail-closed, but not the same as reading it. After it, `collectJSONVariable` and
`jsonValidations` publish each declaration as the author wrote it; JSON variables merge into
`Module.Variables` with a `JSONDeclared` flag; the mutation surface reads
`Module.NativeVariables()` so a JSON declaration never adds or removes a mutant; the engine
widens the characterise and todos sources with the read JSON files so a TODO quotes a JSON
constraint verbatim with its `main.tf.json` range; and the render-and-re-parse step
(`reparseArgument`, `parseNative`) is gone.

Measured against the base binary on the new fixtures: a module whose `.tf.json` validation is
a template directive characterises — one variable resolved from its declared type, the other
the single judgement point quoting the directive — where the base commit failed at plan time
with `No value for required variable`; a `.tf.json` `contains` validation is mined and pinned
where the base commit was red; an all-JSON module writes a suite `terraform test` passes and
previews to zero mutants; the JSON safety floor refuses malformed, unmodelled,
provisioner-bearing and unmocked-provider `.tf.json` identically on both binaries. Three of
the five new cases fail on the base commit; the two population-invariance cases pass on both,
which is what they are for. Five fixtures (`json-child-variable`, `json-sensitive-variable`,
`untested-json-mined`, `untested-json-type`, `untested-json-validation`) are registered in
`tools/json-files`.

One defect the review caught before it landed: the first version guarded `generateCalls`
against JSON-declared variables but not `sensitiveVariables`, so a native `sensitive = true`
output reading a JSON-declared sensitive variable would have lost its `OUT-SENSITIVE-FLIP`
mutant. `TestAJSONDeclaredSensitiveVariableRemovesNoMutant` and its fixture exist because of
that finding.

### 6.2 Other observable differences the record states

#86 put "any change to observable behaviour, other than the single JSON-representation
improvement" out of scope, and every landing was measured against that. The record
nevertheless states four further differences, each a refusal where there was none or a miss
where there was a replay, and none a change to a verdict, a metric or a report a valid
invocation produces:

- **Parse-time refusals of flags the binary previously accepted and ignored.** #97 moved the
  nine population flags under `run`, `preview` and `suggest` only, so `--sample` on
  `characterise` or `--since` on `todos` is now `errInapplicableFlag` with exit 2; on `curate`
  and `--until-dry` the refusal that a runtime population sweep used to make now arrives from
  the parser, before the module path is read. #100 did the same for the six gate flags on
  `preview`, `characterise` and `todos`, where pull request #145 states they "were previously
  accepted and ignored". This is the structural half of DDD-3 — "invalid flag/mode
  combinations ... cannot be represented by a production caller" — and it is observable as an
  exit code where there was silence.
- **`--until-dry` and `--write-baseline` refuse an unobserved population.** #104's acceptance
  criteria say "every refusal message is unchanged", and every pre-existing message is. But
  `curate` was the only one of the three consumers that refused a population with timeouts
  or execution errors before; pull request #148 states the change plainly — `--until-dry`
  "now refuses a round with timed-out or unevaluable mutants (`ErrUntilDryPopulation`) instead
  of counting survivors it never observed", and `--write-baseline` over such a run "is
  refused with the new `ErrBaselineUnobserved` rather than a `--since`/`--sample` remedy that
  does not apply" — and shows the base binary reporting `stop_reason dry` on the same
  60-second-sleeping Terraform wrapper the target refuses. The programme's own population
  proof made the omission visible: a value constructible only from an observed population
  cannot be handed a round that was not. The until-dry refusal propagates as exit 2 with no
  report, matching curate's posture; the review noted the loop's comment that a refused stop
  reason "has to reach a reporter" is now false for that path.
- **An illegal cache record is a miss.** #103's posture is "consistent with the existing
  corruption posture", and the on-disk format and keys are unchanged, but the base binary
  replayed a `Killed`-with-diagnosis entry and the target re-executes it (§3.2).
- **`skill install` refuses a target changed inside the rename window.** `ErrTargetChanged`
  (#91) is a new refusal path the shared primitive's `commit` closure made possible; no
  existing test changed, and the record notes the race has no CLI affordance to drive live.

### 6.3 What was measured byte-identical

Each of these pull requests built the base commit's binary and the target's and compared
their output on real Terraform v1.15.8; the comparison is the programme's regression proof
beyond the gates:

| Pull request | Compared |
| --- | --- |
| #133 (#101) | survivor, unassertable and unobservable verdicts over six fixtures; terminal report diff of zero lines |
| #135 (#102) | `run` and `preview` JSON over 24 fixtures; suppression, exclusion, min-score, tier and timeout-factor variants; all seven reporters on the contract fixture |
| #137 (#112) | `preview` over five fixtures including all 224 generated diffs on the operator matrix fixture |
| #140 (#113) | `characterise` over four modules exercising mining, type synthesis, the nesting limit and a JSON-typed variable |
| #141 (#114) | `run` over the conditional and static-unobservable fixtures; `todos` quoting `can(cidrnetmask(var.vpc_cidr))` verbatim |
| #142 (#106) | every pin skip reason, `--write`, `--until-dry`, `--pin outputs|counts|configured` |
| #143 (#97) | the `curate`, `characterise`, `--until-dry` and `todos` reports under the typed requests; the only difference the build version in a generated header |
| #145 (#100) | every command's JSON report under the contracted request set |
| #144 (#115) | `preview`, `run`, `suggest`, `characterise` over six fixtures under the retyped boundary |
| #146 (#108) | scaffolded, promoted, refused, unparsable and multi-scenario runs |
| #134 (#105) | suggestion output on three fixtures across JSON, terminal and SARIF |
| #138 (#103) | an unchanged second run over a cache the base binary wrote: identical verdicts and cached provenance under the same key; the base binary replaying the illegal entry the target misses |
| #150 (#116) | the mutant population on the JSON fixtures; the JSON safety floor's refusals |

The residual differences those comparisons reported — Terraform's diagnostic order under
`KilledByError`, a random mutant value, `baseline.duration_ms` — reproduce run-to-run with
the target binary alone and are pre-existing nondeterminism, not the programme's.

## 7. The exit position against the review's rubric

#85 scored the fixed point 4.0/10 and projected 9.5/10 after the four increments, reserving
0.5/1 on domain events "because synchronous immutable result facts deliberately stop short
of event infrastructure. A 10/10 is not worth an event bus." Nobody in the record has
re-scored the tree; the review was a fixed-point document and the programme did not
commission a second. The position below is therefore stated criterion by criterion against
what the tree now enforces, with the honest qualifications, and the total is left to the next
review.

| # | Criterion | Position at `fd5a2d3` |
| --- | --- | --- |
| 1 | Names understandable to a domain expert | Unchanged from full credit; the review credited the vocabulary and the programme single-sourced it in `CONTEXT.md`'s glossary. |
| 2 | Bounded contexts explicit | Six named contexts with an owned glossary; the direction enforced by `depguard` over eight package globs including tests. |
| 3 | Aggregates small, transaction boundaries narrow | The exported input is six closed request types; `curate` cannot carry a population by construction. `report.Report` remains one union DTO by decision (§8), pinned per command by `blocks_test.go`. The review's projection gave full credit here on the increments as specified, which included leaving the DTO one union. |
| 4 | Domain objects behaviour-rich | Outcome and evidence, candidate and suggestion, judgement point, pin, scaffold and scenario are constructed and transitioned, never assigned; the published DTOs are now projections. |
| 5 | Domain events across aggregate boundaries | **0.5/1 by design** (ADR-0003). The handoffs return immutable context-owned values, and the four named facts #86 sketched were not minted (§3.3). Whether a reviewer scores the unnamed form the same half point is theirs to decide; the deliberate part is the absence of infrastructure, which stands. |
| 6 | Anti-corruption layer at every external integration | The discovery boundary publishes `hcl.Expression` with two named fail-closed routes to native syntax; JSON declarations are read rather than re-parsed; no domain context imports the publication DTO. |
| 7 | Core subdomain identified | Unchanged from full credit. |
| +1 | Rich core-domain model | Unchanged from full credit, and the core now owns its own outcome type. |
| +1 | Invariants inside aggregates, not services | Every state, diagnosis and evidence combination the domain expresses is legal by construction; the cache path is closed; population authority and freshness are values; the projection is proven total. |
| +1 | Ubiquitous language consistent | The drift #85 listed is repaired and held by `TestTheDocumentedVocabularyMatchesTheBinary`; the two documented deprecations are named exemptions in the totality proof. |

The half point deliberately not chased is criterion 5's. The record adds one qualification
the review's projection did not anticipate: the result facts exist as the contexts' own
types rather than under the names the specification proposed. That is a naming choice the
tree made and this document records; it is not the event bus the review declined.

## 8. What was deferred, and the condition on each

- **#82's fourth synthesis rung.** The diagnostic-driven repair rung of #75's preference
  order is still not built; #90 marked it "specified but not implemented" in
  `docs/design/characterisation.md` §3.2 with a link, and #86 put the decision out of scope.
  What it needs before it is `ready-for-agent`, in #82's words: a measurement in the shape
  standing rule 4 requires — how often a diagnostic-driven repair would resolve an input the
  three built rungs leave as a judgement point — over the corpus in
  `research/corpus/m45-synthesis.json` through `just measure-synthesis`; if the rate
  justifies the rung, the bounded repair table and the fixture that gates it. Either building
  the rung or amending the preference order to the three rungs that exist closes it.
- **A custom expression model.** Not introduced. The condition that would justify one is
  ADR-0004's: a measured consumer served by neither `hcl.Expression` nor the named native
  accessors. #111's census found none among 17 sites, and #116's two point-of-use re-parses
  were served by adding a second named accessor rather than a model. The question reopens
  through its own issue with a new measurement attached, not by argument.
- **Per-assertion provenance.** Assertion provenance is still decided at file granularity by
  the registry's content digest; editing one assertion reclassifies the whole file as
  `generated-edited`. A per-assertion registry needs per-assertion digests written at
  generation time and is the prerequisite for any future `curate --apply`, which M4.5 deferred
  and this programme did not touch.
- **Whether the validation-mining rung is worth its code.** It fired zero times and was
  reached four times over the M4.5 corpus. The programme changed one input to the question
  without answering it: after #116 the rung reaches `.tf.json` conditions as well as native
  ones, so the population it can fire on is larger than the one measured. No new
  measurement was taken; the alternative — sending the constraint straight to the reader as a
  judgement point — is what happens anyway when mining fails.
- **Splitting `report.Report` into per-command documents.** A schema break, and out of scope
  by decision. The achievable part landed: each command populates only its own blocks, held
  by six seam tests, at schema `2.3.0` with no field added, renamed or removed.
- **Two run request types so that `--write-baseline` and `--since` are mutually
  unspellable.** Deliberately not done (#104): one guard is not worth two request types, and
  `freshPopulation` makes the writer unreachable any other way.
- **A reported, exit-1 form of the until-dry population refusal.** Pull request #148's review
  noted the mid-loop refusal discards its block and exits 2 with no report; no change was
  asked for unless a reported refusal is wanted. The record leaves it there.
- **Where the `Population.Cached == 0` rule is stated.** `acceptance.go` still folds it into
  `full` for the staleness judgement and the scope label, and `newFreshPopulation` states it
  again; the review recorded the duplication as deliberate rather than drift. Both are
  needed today.
- **The bootstrap and an ambient `GOROOT`.** Ten of the programme's pull requests record
  having to unset a shell-profile `GOROOT`/`GOBIN` pointing at Go 1.24.13 before the pinned
  1.26.6 toolchain would build, and `scripts/doctor`'s exact mise pin (2026.8.6) fails on a
  host with 2026.8.8 unless the pinned binary under `.artifacts/mise-bin` is on the path.
  Neither is a finding about the product; both cost every ticket minutes. The record does not
  propose a fix, and this document does not either.

## 9. Disposition of `13-m45-exit-gate.md` §14

The M4.5 exit gate carried five open questions into this programme. #116's document review
asked this document to dispose of the `hcl.Expression` bullet explicitly rather than rewrite
a dated record; the other four are disposed of here the same way.

| §14 question | Disposition |
| --- | --- |
| Scaffold promotion and `suggest --apply` are two verify-then-write protocols; should they be one? | The write half is answered by #91: one primitive, three precondition sets (§3.5). Whether the two *verification* legs should share more than they do, the record is silent; the question carries forward in that narrower form. |
| Is validation mining worth its code? | Deferred, with the one new input recorded in §8. |
| Per-assertion provenance | Deferred, unchanged (§8). |
| Should `discovery.Attribute` carry `hcl.Expression`? | Answered: yes, by measurement (#111), contract (#115) and reading (#116). §2.1 and §3.4. |
| Three write protocols side by side; one shared protocol? | Answered by #91 in the direction #85 argued: share the checked atomic-replace primitive only, never the preconditions (§3.5). |

## 10. What the record teaches, beyond the code

1. **A specification that names a signature or a fixture states a hypothesis the tree gets to
   refute.** Several tickets landed with the invariant they asked for in a different shape
   from the one they wrote down (§5). None was a defect; forcing the ticket's words onto the
   tree would have been — a `PinSkipped` that could not say which scenario it skipped, a
   type-constraint fixture no Terraform-legal JSON can produce.
2. **A count in a ticket is a claim about a fixed point, and the fixed point is checkable.**
   #83's twenty-four was corrected to fourteen by re-reading the tree (§2.2). #86's "around
   115" does not reproduce and was superseded by a census the compiler performed (§2.1).
   Where this document could recount, it did, and it says where the recount disagrees.
3. **The invariant, once structural, finds the omission the flags had hidden.** #104's
   population proof could not be handed an unobserved until-dry round, and the refusal that
   followed is the first the record holds for `--until-dry`; before it, such a round was
   declared dry (§6.2). The review of #109 found a totality proof vacuous in one direction and
   seeded it until it was not.
4. **One named accessor became two, and the document moved with the code.** #116's review
   found the re-parse had grown two private routes inside a context; the repair collapsed
   them onto a named discovery accessor and amended the ADR, `AGENTS.md`'s implementation map
   and the boundary's doc comments in the same change, because the precedence rule says the
   losing document is fixed where the conflict is made (§3.4).
5. **The per-ticket status paragraph was the programme's most expensive line of prose**
   (§2.4). A status that every ticket must rewrite is a merge conflict with a schedule.
6. **The document-to-test audit earns its keep on a document like this one.** Every
   `Test…` token above resolves to a declaration in the tree, because
   `TestEveryTestNameADocumentClaimsExists` will not let it be otherwise — the M4.5 lesson,
   applied to the M4.5 lesson's own successor.
