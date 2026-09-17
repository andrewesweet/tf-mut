# M5-0.5a — the opportunity census and the mining count

The static opportunity census over both pinned corpora — the M4.5 synthesis corpus and
the M5 benchmark corpus — run on 17 September 2026 with Terraform v1.15.8 available
(`mise exec -- just measure-opportunities`, seconds of wall clock: the census runs no
Terraform at all).

It answers the only two questions #156 is allowed to ask. First, **#82's first branch**:
do the three shipped rungs leave fewer than ten unresolved inputs across both corpora —
and if not, the stage-2 repair prototype (#163) is confirmed with this census as its
measured input. Second, **the mining rung's count**: how often the rung is consulted and
how often it fires, split by declaration stratum, which decides whether a removal
proposal is licensed. An **opportunity** is one unresolved input, deduplicated by module
and input, classified by the census's closed vocabulary; the census also publishes how
many modules carry at least one.

- Corpus manifests: [`research/corpus/m45-synthesis.json`](../../research/corpus/m45-synthesis.json),
  [`research/corpus/m5-benchmark.json`](../../research/corpus/m5-benchmark.json)
- Harness: `internal/engine/opportunity_integration_test.go`
  (`TestTheOpportunityCensusOverThePinnedCorpora`, integration-tagged), with the
  classification vocabulary, the denominator rules and the offline gates in
  `internal/engine/opportunitycensus_test.go`
- Recipe: `just measure-opportunities` (requires `TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1`,
  which here licenses archive fetching and nothing else — no census request sets a
  product safety opt-in)
- Published output: `.artifacts/measurement/m5-opportunity-census.json`
  (per-row side-car: `m5-opportunity-census-rows.jsonl`, crash-safe across resumes)

## The claim licensed, and the one that is not

The census licenses exactly this claim: **the static opportunity census over the pinned
corpora — and nothing about what a plan would have decided.** Every classification below
comes from the `todos` posture, which parses the pinned modules and lists judgement
points without evaluating a single configuration against a provider. Where a constraint
is undecidable statically, the census says so and stops; whether Terraform would accept
a synthesised value at plan time is #163's prototype to measure, not this census's
claim to make.

## The pin and the method

Both corpora are the manifests pinned by their earlier measurements (#155's benchmark
manifest digest-verified commit archives; #75's synthesis-corpus tag archives). The
harness fetches each pinned archive once (sha256-verified), then drives the engine seam
in the `todos` posture — `TodosRequest`, which runs no Terraform — once per module
directory named by the manifest, and once more per module whose listing leaves
candidates: the classification probe re-issues the listing with an `--answer` carrying
the refused typed candidate, and the outcome splits the fourth rung's gaps into
undecidable constraints (answered), refused typed candidates (refused again), and
withheld evidence (the sensitive-redaction outcome, published with its diagnostic
redacted). The offline gates pin all four classifications, the denominator rules, the
unmeasured-stratum rule and the rows-to-strata fold against fixture modules; over the
live rows the harness asserts one identity the manifests cannot vouch for alone — each
benchmark module's recorded `json_declared` flag, re-checked against the pinned
directory.

The denominator is the root module's declared inputs as the discovery boundary
publishes them — native and `.tf.json` together — and each opportunity carries the
syntax stratum its variable was declared in. The mined-rung count folds to two numbers
per stratum: **reached**, the inputs the rung was consulted for (every input that did
not resolve from its own default), and **fired**, the inputs it resolved by mining a
validation. A stratum with no measured module is published as **unmeasured** — a nil
count, never a zero.

## Stage 1: the opportunities

The census measured **33 pinned modules**: 32 measured, 1 refused. The refusal is
`conf-data-processing-architecture-reference`: its pinned archive's module directory
calls seven sibling modules that do not exist in the tree (the `modules/` directory
ships only `keydb` and `keystorageservice`), so the closure does not parse. The M5-0.4
census refused the same module on the same pinned bytes for the same reason, published
there as an unknown population. The census publishes the refusal and counts none of the
module's inputs.

Across the 32 measured modules, **999 declared inputs** produced **14 opportunities over
8 modules**, every one of them in the benchmark corpus:

| | modules | measured | refused | inputs | defaults | mined | typed | todos | opportunities |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| M4.5 synthesis | 10 | 10 | 0 | 697 | 693 | 0 | 4 | 0 | 0 |
| M5 benchmark | 23 | 22 | 1 | 302 | 248 | 0 | 40 | 14 | 14 |
| both | 33 | 32 | 1 | 999 | 941 | 0 | 44 | 14 | 14 |

One arithmetic statement is construction, not evidence, and is stated as such: the
defaults column is *derived* as the remainder of inputs the mined, typed and todo rungs
did not place, so `inputs = defaults + mined + typed + todos` holds by definition
(697 = 693 + 0 + 4 + 0; 302 = 248 + 0 + 40 + 14). What the census measures is the
placements themselves: the mined rung placed nothing anywhere, the typed rung placed 44
inputs, and the todo listing left 14. Every input in the synthesis corpus
resolves from its own default but 4 — `terraform-aws-rds` (1) and
`terraform-google-network` (3) — and both are placed by the typed rung; no synthesis
module leaves an opportunity at all.

The 14 opportunities, all classified **undecidable constraint**:

| module | inputs | opportunity | constraint shape |
| --- | --- | --- | --- |
| aws-platform-starter | 4 | `region_short`, `environment`, `project_name`, `tags` | `length(trimspace(…)) > 0` ×3; `alltrue([for …])` |
| genai-idp-terraform | 4 | `working_bucket_arn`, `encryption_key_arn`, `input_bucket_arn`, `output_bucket_arn` | `can(regex(…))` |
| aws-terraform-infrastructure | 1 | `account_id` | `can(regex(…))` |
| eks-vulnerable-infra | 1 | `tfstate_bucket_name` | `length(trimspace(…)) > 0` |
| psoxy | 1 | `aws_account_id` | `can(regex(…))` |
| server-terraform | 1 | `nomad_server_hostname` | `!can(regex(…))` |
| serverless-architecture-patterns | 1 | `manifest.subsystem` | `can(regex(…))` |
| terraform-datadog-users | 1 | `users` | `alltrue([for …])` |

Two classes the fixtures pin never occurred in the wild: no input was refused by the
static evaluator with a decidable verdict, and no input lacked a typed candidate
altogether. Every wild gap is a constraint the eleven-function evaluator cannot decide —
`can(regex(…))` dominates — which is exactly the class whose honest resolution requires
Terraform's own evaluation. The wild distribution is #163's measured input: the repair
prototype's static-refutation targets (decidably-false candidates) number zero in these
corpora, while the undecidable class numbers fourteen.

## The mined rung: reached 58, fired 0

Every input that did not resolve from its own default reaches the mining rung first:
**58 consultations, 0 resolutions**, across both corpora and both — well, the one
measured — stratum:

| stratum | reached | fired |
| --- | --- | --- |
| native | 58 | 0 |
| `.tf.json` | unmeasured | unmeasured |

The JSON stratum is unmeasured as a whole fact: no pinned module in either corpus
declares a root `.tf.json` variable. The benchmark manifest records
`json_declared: false` for all 23 modules, and the harness re-checks each flag against
the pinned directory — none of the 23 carries a JSON-declared variable — so the
denominator's JSON accounting (999 inputs, all native) is verified against the
directories, not taken from the manifest. The synthesis corpus computes the same fact
directly.

## The decisions #156 required

**#82, stage 1 — the ten-or-more branch runs.** 14 opportunities ≥ 10, so the
stage-2 prototype is confirmed open: #163 carries the census as its measured input, and
#82 stays open under its own protocol. The two texts the fewer-than-ten branch would
have amended — `docs/design/characterisation.md` §3.2's preference order and the
`#75` design record — stand unchanged. The reopen condition the ticket wrote for the
closing branch is moot here; the corpus manifests are digest-pinned, so any later
revision re-runs this census mechanically (`just measure-opportunities`).

**The mining rule — removal proposed.** The rung was reached 58 times (≥ 5) and fired
0 times, which licenses exactly one thing: a proposal, in its own issue, to remove the
rung. That proposal is [#172](https://github.com/andrewesweet/tf-mut/issues/172); it is
a proposal, not a build, and this census claims nothing about the rung beyond the two
numbers above.

## Side observations the census recorded

- `terraform-aws-iam` measures with **zero declared inputs**: its pinned archive root
  ships no `.tf` files at all (the modules live under `modules/` and `examples/`). A
  root that declares nothing is a measured zero, not a refusal — the same reading the
  M4.5 synthesis-rate measurement published for it.
- `terraform-aws-eks` now measures (88 inputs, all defaulted). The M4.5 synthesis-rate
  measurement refused it on its `moved` blocks; the moved-block repair since landed in
  the reader retired that refusal. The corpus did not change; the engine did.
- The harness reuses #155's digest-verified archive cache, so the two corpus
  measurements fetch and verify the same pinned bytes once between them.

## Reproduction

```
MISE_CONFIG_DIR="$PWD/.artifacts/mise-config" env -u GOROOT -u GOBIN \
  TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1 mise exec -- \
  go test -tags=integration ./internal/engine/ -count=1 -v -timeout 2h \
  -run '^TestTheOpportunityCensusOverThePinnedCorpora$'
```

The side-car resumes: rows already recorded are skipped unless refused, so an
interrupted run re-fetches only what it never finished. The offline gates
(`just gate-m5`) pin the census's vocabulary and rules without the network.
