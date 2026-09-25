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
- **Terraform VERSION_PLACEHOLDER**, mutant concurrency **4**, module concurrency **2**.
- **Environment**: WSL2 on the hardware below; providers resolve from the registry through
  the per-module plugin cache; no repository mirror (the corpus modules name real
  providers the mirror does not carry).
- **Hardware**: CPU_PLACEHOLDER, LOGICAL_CORES_PLACEHOLDER logical cores,
  MEMORY_PLACEHOLDER MiB memory, kernel KERNEL_PLACEHOLDER.
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
MODULE_ADMISSION_ROWS_PLACEHOLDER

| Row outcome | Modules |
| --- | --- |
ROW_OUTCOME_ROWS_PLACEHOLDER

- **Admission rate over known populations: ADMISSION_OVER_KNOWN_PLACEHOLDER.**
- **Admission rate over the pinned corpus: ADMISSION_OVER_PINNED_PLACEHOLDER.**
- **Populations unknown: UNKNOWN_PLACEHOLDER** — these modules sit outside every mutant
  denominator below; no complete mutant denominator is claimed while any population is
  unknown.

## The mutant-level table

In Oasis's units, over known populations only. The pooled columns sum the **scored**
modules — Oasis's method; `generated` sums **every known population**, scored or not; and
the known-but-unscored bucket tabulates those populations by row outcome, the analogue of
Oasis's 61 unscorable mutants.

| Metric | Value |
| --- | --- |
MUTANT_LEVEL_ROWS_PLACEHOLDER

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

PER_MODULE_SCORES_PLACEHOLDER

**Known-but-unscored populations, by row outcome:**

| Row outcome | Populations (mutants) |
| --- | --- |
UNSCORED_BUCKET_ROWS_PLACEHOLDER

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
| Mutants generated | 242 | GENERATED_PLACEHOLDER (KNOWN_MODULES_PLACEHOLDER of 23 modules; POPULATIONS_UNKNOWN_PLACEHOLDER populations unknown) |
| Scored | 177 | SCORED_PLACEHOLDER |
| Killed | 44 | KILLED_PLACEHOLDER (plus KILLED_BY_ERROR_PLACEHOLDER caught at evaluation) |
| Survived | 133 | SURVIVED_PLACEHOLDER |
| Mutation score | 24.9% | MUTATION_SCORE_PLACEHOLDER% (unweighted per-module median: MEDIAN_PLACEHOLDER%) |
| Unscorable | 61 (wanted cloud credentials) | UNSCORED_KNOWN_PLACEHOLDER mutants in known-but-unscored populations (see bucket above) |
| Survivor diagnoses | 104 no_coverage (78%), 29 weak_assertion (22%) | DIAGNOSES_PLACEHOLDER |

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
because they are the part `terraform` cannot drift on without the test going red: the two
legs of every module agree on availability and count, refuse identically, and — per
mutant, over every scored module — reached the same verdict twice, on two sandboxes, once
cold and once warm.
