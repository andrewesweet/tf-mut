# M5-0.4 — the benchmark-corpus census

The module-admission census over the pinned benchmark corpus, run on 17 September 2026
against Terraform v1.15.8 (`mise exec -- just measure-census`, 8h wall clock).

It pins the 23 repositories of Oasis's published evaluation and asks the only question
M5-0.4 is allowed to ask of them: which pinned modules does the shipped tool admit into a
scored population? (The harness drives the engine seam, `engine.Run`, that the shipped
binary wraps — the repository's fixed testing decision and the M4.5-0 precedent — so the
rows classify from the typed stage sentinels the seam returns.) Every module gets two invocations, always both, in this order — the
preview invocation, whose own outcome decides **population availability**; the run
invocation (`--no-cache --tier standard`), whose own outcome decides the **row outcome**
in M5d's total vocabulary. Neither is inferred from the other, and the corpus rows below
are the proof that the two facts are independent in every combination the data produced.

- Corpus manifest: [`research/corpus/m5-benchmark.json`](../../research/corpus/m5-benchmark.json)
- Harness: `internal/engine/benchmark_integration_test.go` (`TestTheBenchmarkCorpusCensus`,
  integration-tagged), with the row vocabulary and the offline gates in
  `internal/engine/benchmarkcensus_test.go`
- Recipe: `just measure-census` (requires `TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1`, which here
  licenses archive fetching and nothing else — no census request sets a product safety
  opt-in)
- Published output: `.artifacts/measurement/m5-benchmark-census.json`
  (per-row side-car: `m5-benchmark-census-rows.jsonl`, crash-safe across resumes)

## The claim licensed, and the one that is not

The count of `scored` modules licenses exactly this claim: **the module-admission rate over
the pinned corpus — and nothing about comparability.** The roadmap's M5 paragraph was
narrowed in the same change that ran this census: a side-by-side evaluation on Oasis's axes
with stated limitations, never "directly comparable". One unit mismatch is structural and
is stated here rather than buried: Oasis's evaluation ran at *repository* level over every
test-equipped module root it resolved; this census runs tf-mut's own unit, one module
directory with one suite, chosen per repository by the mechanical rule published in the
manifest (the test-equipped module directory nearest the archive root, with its closure).
The two numbers share axes, not units.

## The pin

Oasis publishes the 23 repository names (its `benchmarks/README.md` and its report draft)
and — usefully — its own raw GitHub-search evidence, which resolves every name to exactly
one org/repo. It publishes no commits, no archive digests and no module subdirectories, so
the manifest pins each repository at its default-branch HEAD on 2026-09-17 with the
SHA-256 of the archive at that commit, and records the resolution's provenance: Oasis's
list is published, so no independent candidate selection was made. `json_declared` — the
manifest's JSON-stratum flag — is **false for all 23**, and no repository in the corpus
carries any `.tf.json` or `.tftest.json` file at all at this pin: the JSON stratum is
empty, so the #82 census's JSON-declared denominator is zero over this corpus.

## What was measured

| Module (census module directory) | Population | Row outcome | Retries |
| --- | --- | --- | --- |
| aws-platform-starter (`bootstrap`) | known, 976 | **scored** | 0 |
| aws-terraform-infrastructure (`environments/dev`) | unknown | operational | 1 |
| cloud-native-deployment-platform (`infra/modules/alb`) | unknown | operational | 1 |
| conf-data-processing-architecture-reference (`coordinator/…/multipartykeyhosting_primary`) | unknown | operational | 1 |
| eks-vulnerable-infra (`bootstrap`) | unknown | real-infrastructure | 0 |
| genai-idp-terraform (`.`) | unknown | real-infrastructure | 0 |
| platform-design (`terraform/modules/argocd`) | known, 185 | **scored** | 0 |
| platform-tools (`terraform/application-load-balancer`) | known, 1,086 | real-infrastructure | 0 |
| psoxy (`infra/modules/aws`) | known, 718 | real-infrastructure | 0 |
| server-terraform (`nomad-aws`) | known, 1,567 | real-infrastructure | 0 |
| serverless-architecture-patterns (`modules/composition/subsystem`) | known, 4,969 | **scored** | 0 |
| terraform-aws-compliance (`.`) | unknown | operational | 1 |
| terraform-aws-lb (`.`) | unknown | real-infrastructure | 0 |
| terraform-aws-oidc-github (`.`) | unknown | real-infrastructure | 0 |
| terraform-aws-security-group (`.`) | unknown | operational | 1 |
| terraform-aws-sonarqube (`.`) | known, 862 | real-infrastructure | 0 |
| terraform-aws-static-site (`.`) | unknown | real-infrastructure | 0 |
| terraform-aws-vpc (`test/basic-usage`) | unknown | operational | 1 |
| terraform-datadog-users (`.`) | known, 34 | **scored** | 0 |
| terraform-duplocloud-components (`examples/configuration`) | known, 737 | real-infrastructure | 0 |
| terraform-mongodbatlas-project (`.`) | known, 610 | **scored** | 0 |
| terraform-postgres-config-dbs-users-roles (`.`) | known, 189 | real-infrastructure | 0 |
| tofu-modules (`nullplatform/account`) | unknown | operational | 1 |

**Module-admission: 5 scored.** Both denominators, because a rate quoted only over the
modules whose populations are known is the posture `curate` refuses in its own population
check: **5 of 11 over known populations, 5 of 23 over the pinned corpus. Unknown
populations: 12.** No row was `scored-incomplete`: every module that ran, ran to a fully
observed population — no timeouts, no unevaluable mutants. No `no-suite`,
`red-baseline`, `unsupported-payload-version` or `unsandboxed-effects` row occurred in the
live corpus; those outcomes are pinned offline instead, by the fixtures the review passes
promoted (`TestTheThreePreviewRefusalsLeaveThePopulationUnknown`,
`TestTheGatedPreviewFixtureHasAKnownPopulationAndAnUnsandboxedEffectsRow`).

## The two facts are independent, in the data as in the design

The four combinations the corpus produced, each decided by its own invocation:

- **known + scored** (5): the expected admission path.
- **known + refused** (6): `platform-tools`, `psoxy`, `server-terraform`,
  `terraform-aws-sonarqube`, `terraform-duplocloud-components`,
  `terraform-postgres-config-dbs-users-roles` — populations of 189 to 1,567 mutants
  generate cleanly, and the run invocation's static real-infrastructure gate refuses the
  module's own suite anyway.
- **unknown + refused** (5): `eks-vulnerable-infra`, `genai-idp-terraform`,
  `terraform-aws-lb`, `terraform-aws-oidc-github`, `terraform-aws-static-site` — the
  preview invocation never produced a report, and the run invocation still decided on its
  own: its content-driven gates run before any Terraform does, so a module whose suite
  asserts over real infrastructure is refused even when its schema could not be fetched.
- **unknown + operational** (7): the module could not be read at all.

The mixed case the ticket named explicitly — a preview that fails operationally while the
run succeeds — cannot occur in this corpus's data (the operational rows' runs fail too),
so it is pinned offline instead, on the `all-killed` fixture with the preview invocation's
binary broken and the run invocation real (`TestAPreviewThatFailsOperationallyWhileTheRunSucceeds`).
The vocabulary's total mapping is pinned by `TestTheRowVocabularyMapsEveryStageSentinel`,
identity-based via `errors.Is`, never textual.

## Facts about this run, not about the modules

Every operational row's reason is published with the row, and they say "this run", loudly:

- **Shared-plugin-cache provider installation** (four operational rows and three of the
  unknown populations): the shared `TF_PLUGIN_CACHE_DIR` failed to install
  `hashicorp/aws v6.65.0` for several modules at once, and some lock files then refused
  the installed set. This is an artifact of one measurement environment, not a property
  of the modules; a per-module cache could produce different operational rows.
- **A remote backend** (`aws-terraform-infrastructure`'s `environments/dev` declares an
  s3 backend): schema fetch demands backend initialization, which the census will not
  fake.
- **A broken local reference at the pin** (`conf-data-processing-architecture-reference`):
  the pinned commit's module references `../../modules/alert_on_quota`, which the archive
  does not contain. The archive is the pin; the census publishes the break rather than
  repairing it.
- **A read-only lock file** (`tofu-modules`): the pin's lock file conflicts with the
  resolved dependencies and init refuses to rewrite it.

The biggest population, `serverless-architecture-patterns`' 4,969 mutants through one
suite, is the unit rule doing what it says: the nearest test-equipped module's closure
pulls the repository's whole pattern library, and the census grades them all through that
one suite in about four hours. That cost is a fact about the unit mismatch above, and a
planning input for M5d's execution budget, not a defect the census hides.

## What this census does not decide

It does not compare kill rates with Oasis's numbers, because the graded quantities differ
(Oasis scores repository-level suites against its own oracle; tf-mut scores
mutation-survival under its own), because the modules graded differ (one module directory
per repository here; every resolved module root there), and because five admitted modules
is a rate, not a benchmark. Those comparisons are M5d's business, on axes stated when its
spec lands.
