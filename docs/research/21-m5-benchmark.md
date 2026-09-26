# M5d — the public benchmark

The pinned-protocol benchmark over the pinned benchmark corpus, published with the two
tables issue #164 names: the module-admission table in modules, and the mutant-level table
in Oasis's units, side by side with Oasis's row and the stated limitations.

- Corpus manifest: [`research/corpus/m5-benchmark.json`](../../research/corpus/m5-benchmark.json)
  — the same 23 pinned repositories as the [M5-0.4 census](17-m5-benchmark-census.md),
  digests and commits unchanged
- Harness: `internal/engine/benchmarkrun_integration_test.go`
  (`TestTheBenchmarkOverThePinnedCorpus`, integration-tagged), with the aggregation,
  portable assertions and document audit offline in `internal/engine/benchmark_test.go`
  and `internal/engine/benchmarkdocument_test.go`
- Recipe: `just benchmark` (requires `TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1`, which here
  licenses archive fetching and nothing else — no benchmark request sets a product safety
  opt-in)
- Published output: `.artifacts/measurement/m5-benchmark.json`
  (per-module side-car: `m5-benchmark-rows.jsonl`, crash-safe across resumes — a resumed
  measurement re-measures only unfinished or operational modules)

## The protocol

Every number below was produced by one invocation of `TestTheBenchmarkOverThePinnedCorpus`
under this protocol, pinned in the published file's `protocol` block:

- **Two legs per module, always in this order.** The **cold leg** deletes the module's
  plugin cache and makes a fresh one, so every provider downloads cold; the **warm leg**
  reuses the cache the cold leg filled, so providers resolve warm. Within each leg the
  preview invocation and the run invocation each get a fresh sandbox.
- **Cache off, standard tier, no packs** on every scored invocation — the default
  population posture, the one the whole-milestone invariance proof pins
  (`TestTheStandardReportOfTheMatrixFixtureIsInvariantAcrossTheWholeMilestone`).
- **Terraform 1.15.8**, mutant concurrency **4**, module concurrency **2**.
- **Environment**: WSL2 on the hardware below; providers resolve from the registry through
  the per-module plugin cache; no repository mirror (the corpus modules name real
  providers the mirror does not carry).
- **Hardware**: 13th Gen Intel(R) Core(TM) i5-13420H, 12 logical cores,
  11,961 MiB memory, kernel 6.6.87.2-microsoft-standard-WSL2.
- **A leg's row outcome is its cold leg's**, after the one permitted operational retry.
  An operational row is published as a fact about this run, never an abort, and never a
  finding about the module.

The **portable assertions** are what the two legs prove about a module, and the test fails
— after publishing everything the legs produced — if any of them breaks:

1. **Population availability and, where known, the count** are the same fact in both legs,
   each decided by that leg's own preview invocation.
2. **The refusal-class row outcome is deterministic** across the legs. The refusal classes
   are decided before any Terraform runs; a refusal that moves between legs is not a
   finding about the module, it is a broken pin.
3. **Per-mutant verdict identity across the scored modules** — state, then canonical
   verdict, for every mutant the two scored legs share. This is the M3b invariance shape
   carried onto real provider schemas at corpus scale.

The **wall clock is the one non-portable publication**: it is recorded per leg, published
on the named hardware above, and asserted about nothing.

## The module-admission table

In modules. Every rate carries **both denominators** — all pinned modules and modules
with known populations — and the unknown-population count is published beside them,
folded into neither. The row outcome is M5d's total vocabulary, classified by the offline
gates' `classifyRow` order (preview failures onto availability, run failures onto the
row).

| Module | Population | Cold row | Warm row | Cold wall | Warm wall |
| --- | --- | --- | --- | --- | --- |
| `aws-platform-starter` | 976 | scored | scored | 50m | 33m |
| `aws-terraform-infrastructure` | unknown | operational | operational | 39s | 14s |
| `cloud-native-deployment-platform` | unknown | operational | operational | 8m | 10m |
| `conf-data-processing-architecture-reference` | unknown | operational | operational | 0s | 0s |
| `eks-vulnerable-infra` | unknown | real-infrastructure | real-infrastructure | 66s | 95s |
| `genai-idp-terraform` | 34,061 | real-infrastructure | real-infrastructure | 3m | 3m |
| `platform-design` | 185 | scored | scored | 120s | 111s |
| `platform-tools` | 1,086 | real-infrastructure | real-infrastructure | 46s | 49s |
| `psoxy` | 718 | real-infrastructure | real-infrastructure | 48s | 43s |
| `server-terraform` | 1,567 | real-infrastructure | real-infrastructure | 45s | 47s |
| `serverless-architecture-patterns` | 4,969 | scored | operational | 7.0h | 9m |
| `terraform-aws-compliance` | 368 | scored | scored | 3m | 3m |
| `terraform-aws-lb` | 1,105 | real-infrastructure | real-infrastructure | 11m | 61s |
| `terraform-aws-oidc-github` | 301 | real-infrastructure | real-infrastructure | 40s | 39s |
| `terraform-aws-security-group` | 201 | scored | scored | 11m | 12m |
| `terraform-aws-sonarqube` | 862 | real-infrastructure | real-infrastructure | 47s | 43s |
| `terraform-aws-static-site` | unknown | real-infrastructure | real-infrastructure | 78s | 72s |
| `terraform-aws-vpc` | 831 | scored | scored | 32m | 30m |
| `terraform-datadog-users` | 34 | scored | scored | 42s | 39s |
| `terraform-duplocloud-components` | 737 | real-infrastructure | real-infrastructure | 10s | 5s |
| `terraform-mongodbatlas-project` | 610 | scored | scored | 16m | 17m |
| `terraform-postgres-config-dbs-users-roles` | 189 | real-infrastructure | real-infrastructure | 11s | 15s |
| `tofu-modules` | unknown | operational | operational | 15s | 15s |

| Row outcome | Modules |
| --- | --- |
| scored | 8 |
| real-infrastructure | 11 |
| operational | 4 |

- **Admission rate over known populations: 8/17 = 47.1%.**
- **Admission rate over the pinned corpus: 8/23 = 34.8%.**
- **Populations unknown: 6** (`aws-terraform-infrastructure`,
  `cloud-native-deployment-platform`, `conf-data-processing-architecture-reference`,
  `eks-vulnerable-infra`, `terraform-aws-static-site`, `tofu-modules`) — these modules sit
  outside every mutant denominator below; no complete mutant denominator is claimed while
  any population is unknown.

Two admission facts moved against the M5-0.4 census on the same manifest, both run facts,
not pin changes: the census could not install providers for `terraform-aws-compliance`,
`terraform-aws-security-group` and `terraform-aws-vpc` and published them operational,
where this run installed them and scored them; and the census's previews failed
operationally for six modules this run's previews resolved, so this run knows 17
populations where the census knew 11. The benchmark pins its own protocol and publishes
its own numbers precisely so that this variation is measured, not assumed away.

## The mutant-level table

In Oasis's units, over known populations only. The pooled columns sum the **scored**
modules — Oasis's method; `generated` sums **every known population**, scored or not; and
the known-but-unscored bucket tabulates those populations by row outcome, the analogue of
Oasis's 61 unscorable mutants.

| Metric | Value |
| --- | --- |
| Generated (Σ known populations) | 48,800 — over 17 of 23 modules, with 6 populations unknown |
| Invalid (excluded, counted) | 1,081 |
| Unobservable (excluded, counted) | 313 |
| Scored set | 6,780 |
| Killed | 264 |
| Killed by error | 1,424 |
| No coverage | 29 |
| Structurally unassertable | 162 |
| Survived | 4,901 |
| Mutation score | **24.9%** = 1,688 / 6,780 |
| Assertion score | 4.96% = 264 / 5,327 |
| Reachability | 97.2% = 6,589 / 6,780 |
| Pseudo-tested resources | 149 findings across the scored modules |
| Unweighted per-module median | 25.0% over 8 scored modules |

The equations, restated beside the numbers — the same arithmetic
`internal/oracle/metrics.go` computes for every report:

- **mutation score = (killed + killed_by_error) / scored** — the scored set, not the
  generated population, is the denominator, on both sides of the comparison.
- **assertion score = killed / (killed + survived + structurally_unassertable + timeout)**
  — what assertions caught, of what assertions could catch.
- **reachability = (killed + killed_by_error + survived + timeout) / scored** — the share
  of the scored set the suite reached at all.
- **generated = Σ known populations, over K of N modules, with N−K populations unknown** —
  the generated count is a sum over the modules whose populations are known, never a
  claim about the corpus as a whole.
- **median = unweighted median of the per-module mutation scores** — one scored module,
  one vote, whatever its size. It is published beside the pooled score because the pooled
  score is dominated by the largest modules, and the median is not.
- **pseudo-tested resources** — the count of the report's `pseudo-tested` findings across
  the scored modules, the M2 finding's corpus-scale restatement.

Per-module pooled rows (mutation score per module; the median above is the median of this
column):

| Module | Population | Scored | Killed | Killed by error | Survived | Mutation score | Assertion score | Reachability |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `aws-platform-starter` | 976 | 850 | 19 | 180 | 595 | 23.4% | 3.0% | 93.4% |
| `platform-design` | 185 | 180 | 9 | 13 | 158 | 12.2% | 5.4% | 100.0% |
| `serverless-architecture-patterns` | 4,969 | 4,276 | 98 | 724 | 3,334 | 19.2% | 2.8% | 97.2% |
| `terraform-aws-compliance` | 368 | 0 | 0 | 0 | 0 | — (368 Invalid) | — | — |
| `terraform-aws-security-group` | 201 | 169 | 19 | 46 | 102 | 38.5% | 15.4% | 98.8% |
| `terraform-aws-vpc` | 831 | 700 | 25 | 161 | 513 | 26.6% | 4.6% | 99.9% |
| `terraform-datadog-users` | 34 | 33 | 4 | 18 | 9 | 66.7% | 26.7% | 93.9% |
| `terraform-mongodbatlas-project` | 610 | 572 | 90 | 282 | 190 | 65.0% | 31.0% | 98.3% |

`terraform-aws-compliance` is the table's instructive row: its suite passes its own
baseline, and every one of its 368 mutants is Invalid — terraform rejects each mutated
module outright — so its scored set is empty and no score is claimed for it. The pooled
score above sums the other seven modules' arithmetic; the pooled scored set of 6,780 is
tiny only against the generated count because four scored modules carry an Invalid share
of their population with it.

**Known-but-unscored populations, by row outcome:**

| Row outcome | Populations (mutants) |
| --- | --- |
| real-infrastructure | 40,626 (9 modules; `genai-idp-terraform` alone is 34,061) |
| operational | 0 |

The run's survivor diagnoses over the 4,901 survivors: no-assertion 1,671 (34%),
indeterminate-unknown-values 1,636 (33%), unasserted 1,226 (25%), weak-assertion 368
(7%).

## The limitations

These are stated **before** the side-by-side, because they bound what a reader may
conclude from it:

1. **Different fault populations.** Oasis generates 242 mutants under its own
   three-surface model (resource body, variable default, locals/tag maps). tf-mut's
   operators, tiers and applicability matrix are a different catalogue producing a
   different — much larger — mutant set over different sites. The two `generated` counts
   are sums over different populations and mean different things.
2. **Different denominators.** tf-mut's mutant table is pooled over the modules whose
   populations are known, and says so; Oasis's 242 is its full generation. Neither number
   normalises onto the other, and the unknown-population count sits outside both.
3. **Different scoring conditions.** Oasis could not score 61 mutants (25%) for want of
   cloud credentials — its runs were real applies. tf-mut's benchmark runs fully mocked
   (`terraform test` with mocked providers), so its scored set measures a different
   agreement between mutant and suite: the plan-and-assert surface, not the apply surface.
4. **Different tools, different kills.** A kill pairs a mutant with a suite through one
   tool's execution and oracle. The pooled mutation score, assertion score and
   reachability are each computed by tf-mut's oracle over tf-mut's runs; Oasis's 24.9% is
   computed by Oasis. Each is a true number about its own pipeline.
5. **One machine, one run.** The wall clocks come from one pinned protocol on the hardware
   named above. They are published to make the protocol concrete, not as a performance
   claim, and no cross-tool timing comparison is licensed.
6. **The corpus is the population.** 23 pinned public repositories, not a sample of the
   Terraform ecosystem. Both tools measured the same pinned commits and digests; that is
   the one thing this benchmark holds fixed, and it is what makes any common-axis reading
   honest.

## Side by side with Oasis

What the axes share, with every limitation above in force. tf-mut's row is the cold legs'
pooled publication from the tables above; Oasis's row is as published in
[`02-prior-art.md`](02-prior-art.md).

| Axis | Oasis (published) | tf-mut (this measurement) |
| --- | --- | --- |
| Repositories | 23 public repositories | 23 pinned repositories (same manifest) |
| Mutants generated | 242 | 48,800 (17 of 23 modules; 6 populations unknown) |
| Scored | 177 | 6,780 |
| Killed | 44 | 264 (plus 1,424 caught at evaluation) |
| Survived | 133 | 4,901 |
| Mutation score | 24.9% | 24.9% (unweighted per-module median: 25.0%) |
| Unscorable | 61 (wanted cloud credentials) | 40,626 mutants in known-but-unscored populations (see bucket above) |
| Survivor diagnoses | 104 no_coverage (78%), 29 weak_assertion (22%) | 1,671 no-assertion (34%), 1,636 indeterminate-unknown-values (33%), 1,226 unasserted (25%), 368 weak-assertion (7%) |

The two mutation scores landing on the same 24.9% is a coincidence of aggregation, not a
finding: limitation 1 makes the numerator and denominator different populations on each
side, and two ratios of different things agreeing to one decimal place licenses nothing.

## What the numbers license

The measurement licenses exactly the claims its tables state: the module-admission rate
over both denominators; the pooled and per-module mutant-level arithmetic over the scored
modules, with both units published; the determinism of the refusal classes across cold and
warm legs; and per-mutant verdict identity across the scored modules' two legs. It does
not license a direct-comparability reading — the limitations above are the reason — and
the roadmap's M5 paragraph carries the narrowed claim: a side-by-side evaluation on
Oasis's axes with stated limitations, pinned and measured before any comparability claim
is made.

The portable assertions are the part of this measurement a reader may trust hardest,
because they are the part `terraform` cannot drift on without the test going red. Here is
exactly what the run's two legs agreed on, published rather than smoothed:

1. **Availability and count agreed on 22 of 23 modules.** The exception is
   `serverless-architecture-patterns`, whose warm leg failed operationally — a provider
   install into the reused cache raced a still-exiting process from the cold leg's seven
   hour execution — so its availability moved between legs and the warm leg published no
   verdicts. The cold row stands as published; the race is a fact about the run, not the
   module.
2. **The refusal classes were deterministic** — all 11 real-infrastructure rows and all
   4 operational rows classified identically in both legs.
3. **Per-mutant verdict identity held completely on six of the eight scored modules** —
   2,058 compared mutants across `platform-design`, `terraform-aws-compliance`,
   `terraform-aws-security-group`, `terraform-aws-vpc`, `terraform-datadog-users` and
   `terraform-mongodbatlas-project`, zero moves. `aws-platform-starter` moved: of its 890
   compared mutants, 853 kept their state (37 state moves) and 647 kept their canonical
   verdict (206 further moves at the diagnosis level). The killed set was perfectly
   stable — 19 Killed and 180 Killed-by-error in both legs — and the aggregate moved by
   three hundredths of a percentage point (23.41% cold, 23.38% warm). The instability is
   concentrated where the M2 exit gate said it lives: survivors on the boundary between
   survived, unobservable and structurally-unassertable, and the diagnosis a survivor
   carries.

The offline gate `TestABrokenLegAgreementTurnsThePortableAssertionsRed` proves the
assertions red-capable on a broken fixture; the live run above is what they look like
when a real corpus trips them. A resumed invocation re-asserts every portable claim over
the side-car and re-measures only unfinished modules and modules whose cold row is
operational — which is how the four operational rows were retried in the resume passes —
and the resumed passes re-produced the same 244 violations identically: the assertions
are themselves deterministic over a fixed set of legs.
