# M1 exit gate — reproduction sweep and real-provider measurement

Milestone M1 (issue #2) declares two exit gates. This document closes both, and records the
deviations honestly rather than quietly.

> Everything measured here was executed against `Terraform v1.15.8 on linux_amd64` with the
> engine at the commit that introduced this file.

---

## Gate 1 — every reproduction case is an executable fixture

Both adversarial reviews left a set of failure modes that had to become permanent members of
the tool's own test suite. Each row below names the case, the fixture that reproduces it, and
the test that asserts the repaired behaviour. All of them are offline and run in the ordinary
`just test` gate.

| Case | Origin | Fixture | Test |
| --- | --- | --- | --- |
| Hardlink write-through mutates the source tree | R2-3 | `testdata/skeleton`, and every other fixture | `TestSourceTreeIsUntouchedByEveryRun`, `TestKillingTheProcessLeavesTheSourceTreeIntact`, `TestWritingThroughAHardlinkToTheSourceIsRefused` |
| `count = 0` against a bare reference is statically doomed | C3 / R2-5 | *unreachable by construction* — see deviation 1 | `TestIndexedConsumerNeverGetsAnInstanceSetDeletion` |
| `count = 0` against an exact-index reference errors at evaluation | R2-5 | `testdata/count-indexed` | `TestIndexedConsumerNeverGetsAnInstanceSetDeletion`, `TestZeroAssertionSuiteReportsNothingAsTested` |
| A `for_each` resource must never gain a `count` | R2-5 | `testdata/foreach` | `TestForEachIsEmptiedAndNeverGainsACount` |
| An emptied instance set is killed where consumers tolerate it | R2-5 | `testdata/count-tolerant` | `TestExistingCountIsEmptiedWhenEveryConsumerTolerates` |
| `KilledByError` must never read as evidence of testing | M8 / R2-5 | `testdata/discriminate` | `TestErrorKillsAreNeverEvidenceOfTesting` |
| Upward (`../`) module sources escape a module-rooted sandbox | M6 | `testdata/upward` | `TestUpwardModuleSourceIsMutatedAndTested` |
| A missing `.terraform.lock.hcl` classifies the whole population `Invalid` | m14 | `testdata/mocked-null` | `TestMissingLockFileStillExecutesEveryMutant` |
| A remote child module is absent from the sandbox | R2-7 | generated git-over-`file://` repository | `TestRemoteModuleIsPresentForExecutionButNeverMutated` |
| A filter matching nothing exits 0 and reads as survival | m14 | `testdata/skeleton` with a selection that matches no file | `TestSelectionMatchingNothingIsAnOperationalFailure` |
| Provisioners execute under mocked apply | R2-10 | `testdata/provisioner` | `TestProvisionerIsRefusedAndNeverExecuted`, `TestProvisionerRunsOnlyWithTheSecondOptIn` |
| Static and dynamic failures emit byte-identical diagnostics | R2-6 | `testdata/discriminate` | `TestStaticAndDynamicFailuresAreDiscriminated` |
| Timeouts excluded from the denominator let load raise the score | R2-11 | `testdata/skeleton` at a one-millisecond budget | `TestTimeoutLandsInTheDenominatorAndFailsTheGate` |
| Overlapping states make counts depend on file order | R2-8 | `testdata/discriminate` | `TestReportIsIdenticalAtEveryParallelismLevel`, `TestMetricsAreReproducibleFromTheStateCounts` |
| A red baseline lets mutation results mislead | §3 Baseline | `testdata/red-baseline` | `TestRedBaselineAbortsNamingTheFailingRun` |
| A baseline executing zero runs reports every mutant survived | §3 Baseline | `testdata/empty-tests` | `TestBaselineWithoutRunBlocksAbortsLoudly` |
| The JSON report drifts from the schema consumers parse | #16 | `docs/schema/report-1.0.0.json` | `TestReportValidatesAgainstThePublishedSchema`, `TestReportSatisfiesThePublishedSchema` |
| A worker dying takes the population with it | #15 | `testdata/skeleton` with a Terraform that dies for one sandbox | `TestOneFailingWorkerDoesNotPoisonTheOthers` |
| A resource Tier 0 cannot reach vanishes from the headline | Story 1 | `testdata/no-optional-arguments` | `TestResourcesWithoutAnExtremeMutantAreReported` |

### Implicit shared run-block state (R2-4)

Not reproduced, and deliberately so. R2-4 constrains **run-block file splitting**, which is an
M2 feature; M1 executes the whole suite for every mutant, so the semantics R2-4 describes are
preserved by construction rather than by repair. The seam the constraint will attach to exists
(`Config.TestSelection`, applied to mutant execution and not to the baseline), and the
reproduction lands with the feature. Recorded here so that M2 cannot start by assuming the
case is already covered.

---

## Deviations from the milestone spec

Four, each with its reasoning. The precedence rule (review dispositions > milestone spec >
design prose) decided the first two.

### 1. `count = 0` against an indexed consumer is never generated

Issue #14 describes a reproduction fixture in which `count = 0` "against indexed references"
is *generated* and classifies `KilledByError`. The R2-5 disposition says the opposite:
"exact-index consumers get `EXT-BODY-BLANK`", and the normative multiplicity table in the
milestone spec itself says an existing `count` is emptied only when *all* consumers tolerate
empty collections. The disposition and the table win, so the implementation never generates
that mutant.

What the fixture proves instead is the property the case exists to protect: on
`testdata/count-indexed` the fallback `EXT-BODY-BLANK` mutant is the only extreme mutant for
the resource, the suite asserts nothing, and the resource is still reported pseudo-tested. The
honesty requirement — "`KilledByError` never counts as evidence of testing" — is asserted
directly on `testdata/discriminate`, where the extreme mutant for `terraform_data.app` *does*
classify `KilledByError` and the resource is still listed as pseudo-tested.

A bare consumer of a counted resource cannot exist in a green baseline at all: Terraform
rejects `terraform_data.app.id` the moment `app` has `count`, whatever its value. The C3 case
is therefore unreachable for `EXT-RESOURCE-DELETE` once the operator refuses to *add* a
`count`, which it does.

### 2. `Invalid` is reachable only through module inputs

Within Tier 0, with a green baseline and the provider-schema gate in place, a statically
invalid mutant is close to unreachable: the operators only delete, deleting a schema-optional
argument yields a null value rather than a static error, and deleting a `count` or `for_each`
is out of scope. The one remaining source is `EXT-MODULE-INPUT-DELETE` against a child
variable with no default, which `terraform validate` rejects with "Missing required argument"
(verified).

That operator is therefore **not** gated on the child's default. Gating it would be the
cheaper choice, but user story 6 of the milestone spec ("statically invalid mutants discarded
and excluded from all scores") only has meaning if such mutants can occur, and the
lazy-validate discriminator that R2-6 forced into the design would otherwise be untested. The
cost is one sandbox and one `validate` per required module input.

### 3. Preview initialises one workspace

Issue #14 asks that `preview` "executes no Terraform beyond schema retrieval and touches no
sandbox". It materialises no *mutant* sandbox and executes no test. It does, however: copy the
local-module closure once into a temporary warm workspace, run `terraform init` there, run
`terraform providers schema -json`, and run `terraform fmt -check`. The init is unavoidable —
`providers schema` needs an initialised working directory, and the source tree is never
written to — and the copy is what keeps that true. The `fmt` check is not required by preview
and is retained only because it shares the preparation path; it is read-only.

Preview also skips both safety gates, which #11 does not discuss. Refusing a preview would
hide the mutant population from exactly the person deciding whether to accept the risk, and a
preview executes no run block. `TestPreviewIsNeverRefusedBecauseItExecutesNothing` asserts the
provisioner still does not run.

### 4. One test replaces the Terraform binary

`AGENTS.md` fixes the testing seam with "No fake Terraform runner". One test —
`TestTerraformBelowTheTestFrameworkIsRefused` — points `Config.TerraformBinary` at a four-line
shell script that answers `version -json` with `1.5.7` and nothing else. The alternative is to
install an obsolete Terraform release in the build chain to prove one refusal message.

This is a stub of the *binary*, not of the engine's Terraform integration: no other test
replaces it, and nothing about mutation behaviour is asserted through it. The related worker
crash test (`TestOneFailingWorkerDoesNotPoisonTheOthers`) wraps the *real* binary and makes it
die for one sandbox, which is a fault injection rather than a fake.

---

## Gate 2 — the real-provider measurement

The standing process rule from round one: *no performance or feasibility claim enters a design
document until it has been measured against a realistically-sized provider schema.* The
fixture is `internal/engine/testdata/aws-mocked`: ten resources across S3, SQS, SNS,
CloudWatch Logs, IAM and DynamoDB, a `mock_provider "aws"` block, three plan-mode run blocks
and six assertions. `hashicorp/aws` 6.x installs at roughly 840 MB on disk.

### Method

The measurement drives the ordinary engine entry point — the same code path `tf-mut run` uses.
It is network-gated and kept out of the offline suite.

Hardware: 13th Gen Intel Core i5-13420H, 12 logical cores, WSL2 on Linux 6.6.

### Result

Three invocations were taken: one with the provider freshly installed, one warm, and one
re-verification against the final build. All three executed the identical population.

| Quantity | Cold | Warm | Final build |
| --- | --- | --- | --- |
| Mutants generated | 40 | 40 | 40 |
| Mutants executed | 40 (no `NoCoverage`) | 40 | 40 |
| Baseline suite | 2.40 s | 1.88 s | 1.86 s |
| Wall clock at `--jobs 8` | 149.2 s | 116.4 s | 128.4 s |
| Throughput | 0.27 mutants/s | 0.34 mutants/s | 0.31 mutants/s |

Call it **~0.3 mutants/s**, with roughly 10% run-to-run variance on an otherwise busy laptop.
The verdicts were identical in all three: mutation score **28.9%**, assertion score **22.9%**,
reachability **100%**, and **6 of the 10 resources reported pseudo-tested**.

That last number is the milestone's whole point, and it behaves on a realistic module: six
resources are planned by the suite and asserted on by nothing, on a suite that passes.

### Comparison with the design's targets

The round-one review measured a comparable module — ten resources, fully mocked — at **~0.36
mutants/s** for plain `-json` execution and **~0.10 mutants/s** for the naive per-mutant
`validate` + `-verbose` sequence. The engine as built lands at **~0.3 mutants/s**: level with
the plain figure, and roughly 3× the naive sequence, which is the lazy-validate design working
as intended. Nothing here is faster than the review's plain number, and nothing should be: M1
has none of the speed levers.

The design's stance is unchanged, and now measured rather than asserted: a full sweep of a
real-provider module is **minutes**, not seconds. Forty mutants took a little over two minutes; the operator
catalogue estimates 150–250 Tier 0 mutants for a 500-line real module, which extrapolates to
eight to fourteen minutes. That is scheduled work, exactly as `product-design.md` §2 says.

### Parallel scaling — the finding that matters

Measured on the same population within one invocation, warm:

| Fixture | `--jobs 1` | `--jobs 8` | Speedup |
| --- | --- | --- | --- |
| `aws-mocked` (40 mutants, `hashicorp/aws`) | 125.9 s | 116.4 s | **1.08×** |
| 20 `terraform_data` resources, 20 outputs (40 mutants, no provider) | 2.13 s | 0.70 s | **3.0×** |

The offline fixture also reaches 0.63 s at `--jobs 12` (3.4×, ~63 mutants/s), consistent with
the spike's 43 mutants/s on eight cores.

**Parallelism buys almost nothing against a large-schema provider.** The spike concluded the
run is "CPU-bound (`user` ≈ 6× `real`), so it scales with cores" — measured against
`hashicorp/null`, whose schema is 3.4 KB. Against `hashicorp/aws` the marginal cost of a
mutant is not the plan; it is starting an 840 MB provider plugin once per run block and
handing its schema to Terraform. Eight of those concurrently saturate memory bandwidth long
before they saturate twelve cores.

This is a new constraint on the roadmap, and it is worth stating plainly because it changes
where M2 should spend effort: `--jobs` is a lever for small-schema modules, and **test
selection is the only lever that matters for real-provider ones**. It also sharpens C1's
finding rather than repeating it — C1 was about `-verbose` re-serialising the schema into
every message, which M1 never does; this is the plugin process itself.

Recorded against issue #15's acceptance criterion "wall-clock improvement demonstrated on the
suite's largest fixture": demonstrated at 3.0× on the offline corpus, and honestly reported as
1.08× — effectively nothing — on the real-provider fixture.

### Reproducing

```bash
TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1 TF_MUT_MEASURE_SCALING=1 \
  go test -tags=integration ./internal/engine/ \
  -run TestPerformanceAgainstAMockedRealProvider -count=1 -timeout 40m
```

Numbers land in `.artifacts/performance/m1-exit-gate.json`. The offline scaling pair is a
direct `just build` plus two timed `tf-mut run` invocations at different `--jobs`.

---

## Definition of done

| M1 requirement | State |
| --- | --- |
| R1/R2 reproduction fixtures pass as the tool's own test suite | Met, with the R2-4 exception recorded above |
| Real-provider re-measurement published | Met, above |
| Tier 0 operator catalogue, schema-gated | Met |
| Lazy-validate classification with explicit precedence | Met |
| Three metrics, timeouts in the denominator, incomplete-score semantics | Met |
| Safety gates for real infrastructure and unsandboxed effects | Met |
| Sandbox contract: closure rooting, sharing rules, fresh-inode writes | Met |
| Terminal and JSON reporters from one report value | Met |
| JSON schema published and versioned, validated in the suite | Met — `docs/schema/report-1.0.0.json` |
| Parallel execution bounded by `--jobs` | Met; the wall-clock benefit is real offline and near zero against a large-schema provider, measured above |
| Pseudo-tested headline defined over assertion kills only | Met |
