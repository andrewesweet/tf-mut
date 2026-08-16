# M1 implementation review — 16 August 2026

What implementing milestone M1 ([issue #2](https://github.com/andrewesweet/tf-mut/issues/2),
[PR #18](https://github.com/andrewesweet/tf-mut/pull/18)) taught, recorded the way the
build-chain implementation review was, so that the next milestone spec absorbs it rather than
rediscovering it.

Two kinds of entry appear below, and the difference matters:

- **Measurements** are facts. Standing process rule 1 gives them force over design prose, and
  the losing documents have been corrected in the same change.
- **Decisions** are what the implementation did about them. They are open to revision by a
  later spec, but a spec that contradicts one should say so deliberately.

The recommendations that follow from either are collected at the end as **open questions**,
not as dispositions. They are the M1 implementer's judgement and carry no precedence; the M2
spec author is expected to dispose of each, in either direction.

---

## Measurements

| # | Measurement | Consequence |
| --- | --- | --- |
| M1-A | **Parallelism is worth 1.08× between one job and eight** on a fully-mocked `hashicorp/aws` module (125.9 s → 116.4 s, 40 mutants), against **3.0×** for an equivalent provider-free population (2.13 s → 0.70 s), and 3.4× at twelve jobs. | The spike's "CPU-bound … so it scales with cores" holds only for small-schema providers, and is corrected in `04-harness-spike.md` §Q5. The marginal cost of a mutant against a large provider is starting the plugin process once per run block, not the plan. `--jobs` is a small-schema lever. |
| M1-B | **Throughput is ~0.3 mutants/s** on that fixture at eight jobs (0.27 cold, 0.34 warm, 0.31 re-verified; ±10% run to run). | Level with round one's plain-`-json` figure (~0.36) and roughly 3× the naive per-mutant `validate` + `-verbose` sequence (~0.10). The two-phase and lazy-validate decisions are doing what C1 and M11 predicted. Published with method and hardware in `06-m1-exit-gate.md`. |
| M1-C | **`Invalid` is nearly unreachable in Tier 0.** With a green baseline and the schema gate, deleting a schema-optional argument yields a null value rather than a static error, a bare consumer of a counted resource cannot exist, and `EXT-RESOURCE-DELETE` never adds a `count`. The single remaining source is `EXT-MODULE-INPUT-DELETE` against a child variable with no default, which `terraform validate` rejects with "Missing required argument". | Lazy validation costs almost nothing in M1 because almost nothing errors. That is a property of Tier 0, not of the design, and it does not carry into M2. |
| M1-D | **`terraform test` reports skipped run blocks with the same completion message as executed ones**; only the status discriminates. A parse-level failure produces diagnostics and *no* `test_run` or `test_summary` messages at all. | The executed-run count is derived from statuses, not from `test_summary`, and a zero-executed-run result is disambiguated by `validate` before it can be called an operational failure. |

## Decisions

| # | Finding | Disposition |
| --- | --- | --- |
| M1-1 | Issue #14 asks for a fixture in which `count = 0` **is generated** against an indexed consumer; R2-5's disposition and the normative multiplicity table in #2 both say exact-index consumers get `EXT-BODY-BLANK`. | **Resolved by the precedence rule** in favour of the disposition: that mutant is never generated. The property the case protects — `KilledByError` is never evidence of testing — is asserted directly instead, on a resource whose only extreme mutant genuinely classifies `KilledByError`. |
| M1-2 | Gating `EXT-MODULE-INPUT-DELETE` on the child variable's default would remove Tier 0's only source of a statically invalid mutant. | **Not gated.** User story 6 of #2 ("statically invalid mutants discarded and excluded from all scores") is meaningless if none can occur, and R2-6's lazy-validate discriminator would be untested. Cost: one sandbox and one `validate` per required module input. |
| M1-3 | `preview` cannot retrieve provider schemas without an initialised working directory, and the source tree is never written to. | **Preview materialises one warm workspace** and runs `init`, `providers schema` and `fmt -check` there. It materialises no mutant sandbox and executes no run block. It also **skips both safety gates**: refusing a preview would hide the mutant population from the person deciding whether to accept the risk. |
| M1-4 | Proving the sub-1.6 version refusal requires a Terraform release the build chain does not pin. | **One test replaces the binary** with a four-line script answering only `version -json`. A stub of the binary, not of the engine's Terraform integration; the seam's "no fake Terraform runner" is otherwise intact, and the worker-crash test wraps the *real* binary rather than faking it. |
| M1-5 | R2-4 (implicit shared run-block state) constrains run-block file splitting, which M1 does not build. | **Deferred to M2 with the seam in place.** M1 executes the whole suite for every mutant, so R2-4's semantics are preserved by construction rather than by repair, and a reproduction now would assert nothing about the code that will break it. `Config.TestSelection` exists, applies to mutant execution and **never** to the baseline. Recorded so M2 cannot assume the case is covered. |
| M1-6 | The tool handed its whole ambient environment to Terraform. Under an exported `GIT_DIR`/`GIT_WORK_TREE` — which this repository's own mutation gate sets, and which any git hook sets — Terraform's module installer failed on every remote module, and a test fixture's `git init` wrote `core.worktree` into the real checkout. | **Accepted as a product defect.** The runner strips the eight git *location* variables and keeps everything else, because `GIT_SSH_COMMAND` and the credential helpers are how a private module source authenticates. Generalised as standing process rule 4. |
| M1-7 | Adding `hashicorp/hcl/v2` pulled in `golang.org/x/text` v0.25.0; govulncheck traced GO-2026-5970 through `hclsyntax.TemplateExpr.Value` to `norm.Form.LastBoundary`, reached by ordinary module parsing. | **Accepted.** Bumped to v0.39.0. Both this and M1-6 were found by gates rather than by review, which is the argument for keeping `just security` and `just mutate-diff` outside the fast loop but inside the definition of done. |
| M1-8 | The pseudo-tested headline is about managed resources, but `EXT-ATTR-DELETE` also fires on data sources. | **Data sources are mutated and excluded from the headline.** Calling a data source pseudo-tested is a category error. Whether they deserve their own finding kind is an open question for M2. |
| M1-9 | A resource whose every argument is schema-required yields no extreme mutant, and would silently vanish from the headline. | **Reported as a warning** naming the resources the run says nothing about. Tiers 1–3 largely remove the case. |
| M1-10 | R2-3 permits hardlinks only for files that will never be written, but "never written" was not defined. | **Defined as a suffix allow-list** (`.tf`, `.tftest.hcl`, `.tfvars`, and their JSON forms). Everything else is copied, the dependency lock file above all, because `init` rewrites it. |
| M1-11 | The design's `--jobs` default is `NumCPU`; issue #15 asks for "a sensible fraction of cores". | **Three quarters of the cores.** M1-A makes taking every core a poor trade: it buys little and costs the rest of the desktop. |
| M1-12 | The JSON report had no published contract to validate against. | **`docs/schema/report-1.0.0.json`**, validated in the suite, with the version constant and the file name checked against each other. M2 adds states, diagnoses and reporters, so **M2 breaks this schema** and must ship `report-2.0.0.json`. |
| M1-13 | `.golangci.yml` was written for a fifty-line codebase and produced 412 findings against the engine, dominated by `revive`'s `add-constant`, `cognitive-complexity` at 7, and `max-public-structs` at 5. | **Relaxed with a stated reason per entry**, thresholds raised rather than rules disabled where the rule has value. `AGENTS.md` now requires the reason. The linters that catch defects stay on; the tree is at 0 issues. |

## Open questions for the M2 spec

Not dispositions. Each needs an answer from the M2 spec author, and "no" is a legitimate answer
to any of them.

1. **Does run-block file splitting add or remove work on a large-schema provider?** This is the
   most consequential open question in the roadmap. Splitting produces *more* `terraform test`
   invocations, and M1-A says each invocation's dominant cost is plugin startup. The design
   calls splitting "the primary viability lever on real modules"; that claim has never been
   measured against the bottleneck M1 found, and it may be net-negative on exactly the modules
   it was meant to rescue. Three experiments would settle it, all on the existing `aws-mocked`
   fixture: one invocation with *n* run blocks against *n* invocations of one; the marginal
   cost of `-verbose` measured through this harness rather than inferred from C1; and how many
   run blocks instantiation-reachability selection actually removes from a realistic suite.
   Sequencing them **before** the splitting design is standing process rule 1 applied forwards.
2. **M2's exit gate names a feature scheduled for M3.** "A `--since`-scoped run … inside the
   stated time envelope" cannot be met by a milestone that does not contain `--since`. Either
   pull it into M2 or restate the gate over selection alone — and express it as a delta from
   M1's published baseline (0.3 mutants/s, 1.08× scaling) so that "faster" is measured.
3. **What does lazy validation cost in Tiers 1–3?** M1-C says the M11 win comes from Tier 0
   producing almost no errors. Type-changing mutations, weakened validations and boundary flips
   error routinely, and every error buys a `validate` at ~1.7 s on a real provider. M11's
   original disposition — validate only for operators with plausible static-failure modes —
   may need to come back.
4. **Do data sources get their own finding kind?** See M1-8.
5. **Does the unanswerable-resource warning survive?** See M1-9. Tiers 1–3 mostly remove the
   condition that produces it.
6. **How should the report schema's second version be sequenced?** M1-12. Breaking it once,
   deliberately, at the start of M2 is cheaper than breaking it twice.

## Process note

M1-1 cost real implementation time: a `ready-for-agent` issue restated a review disposition in
its own words and inverted it. The precedence rule resolved it correctly, but the conflict was
avoidable. **A milestone spec that restates a review finding should quote it, not paraphrase
it**, and `/to-spec` should check each acceptance criterion against the disposition it derives
from.

The wider pattern from this milestone is that the two defects with the worst blast radius —
writing into the caller's git configuration, and a reachable CVE — were found by the scheduled
gates, not by review or by the fast loop. Round one's lesson was measure against a realistic
provider; round two's was that repairs need the same scrutiny as originals; this one's is that
**the gates the definition of done names are load-bearing even when the fast loop is green**.
