# Adversarial review — 15 August 2026

An independent adversarial review of the design documents was commissioned (Claude Opus,
maximum rigour, empowered to run experiments against the spike fixtures and against a
fully-mocked `hashicorp/aws` 6.20.0 module). Its findings, the disposition of each, and the
resulting design changes are recorded here. The reviewer's evidence was spot-checked before
acceptance; the three cheapest critical claims (C1, C2, C3) were independently re-verified.

**Verdict as delivered:** "No — not as written, though I would fund a revised version of it…
what is missing is a second spike against a real provider and a real module, which would have
caught all four criticals in an afternoon and which should be the condition of funding."

All four criticals were accepted. The design documents have been revised accordingly; this
file preserves the findings and the reasoning behind each disposition.

## Dispositions

| # | Sev | Finding (compressed) | Disposition |
| --- | --- | --- | --- |
| C1 | CRIT | Throughput thesis is a null-provider artefact. `-verbose` re-serialises the full provider schema (14.5 MB for AWS) into every `test_plan`/`test_state` message; measured 0.10 mutants/s for the designed per-mutant sequence on a 10-resource mocked-AWS module vs the claimed 43/s. Marginal run-block cost 26× with `-verbose` | **Accepted.** Execution is now two-phase: every mutant runs plain `-json`; only non-killed mutants re-run with `-verbose` for fingerprinting. All performance claims re-scoped: interactive for small-schema providers and `--since` loops; minutes-to-hours for full standard runs on large real-provider modules. A real-provider spike is now the M1 exit gate. Upstream ask recorded: a `-verbose` variant that omits `provider_schemas` |
| C2 | CRIT | `relevant_attributes` is the prior-state dependency set for refresh, not an attribute→run-block coverage map: identical across run blocks, blind to assertion reads, absent from `test_state`. Test selection built on it would produce silent false survivors | **Accepted.** Verified independently. Test selection and the attribute-coverage line now derive from the assertion inventory (AST of `.tftest.hcl` assertion expressions) intersected with the mutation site's forward cone. Consequence: the reference graph moves from post-MVP to M3 — it is load-bearing, not an optimisation |
| C3 | CRIT | `EXT-RESOURCE-DELETE` via `count = 0` is Invalid exactly when a dependent uses a *bare* reference (`Missing resource instance key`) — the majority case in wired modules — and the design stated the condition inverted | **Accepted.** Verified independently. Operator now gated on a static reference scan (emit only when all dependent references are indexed/splatted); bare-referenced resources get a new extreme operator (`EXT-BODY-BLANK`: delete every optional argument at once) so the pseudo-tested question remains answerable |
| C4 | CRIT | `Unobservable`→Excluded erases Tiers 3–4 (validation, lifecycle mutants are fingerprint-identical), and the `structurally-unassertable` diagnosis is unreachable because survivors require a differing fingerprint | **Accepted.** `Unobservable` split by cause: fingerprint-identical where the construct has no plan/state projection → `StructurallyUnassertable`, a reported state in the denominator with fix guidance (`expect_failures` run block, or accept); fingerprint-identical for a projecting expression → `unobservable-under-current-inputs`, excluded |
| M5 | MAJ | Double-baseline volatile mask: empty in plan mode (computed attrs stay unknown), does not equal "mock-supplied" (`uuid()` in configured attrs), and misses second-quantised volatility (`timestamp()` stable within one clock second) | **Accepted.** Volatile set = static AST scan for impure functions and unmocked `random_*`/`time_*` providers, unioned with the two-run diff. `MockMasked` decoupled from the run-diff: derived from provider-schema `computed` flags, and scoped to apply-mode (plan mode leaves computed attributes unknown, so mock masking cannot occur there) |
| M6 | MAJ | Sandboxes break on `source = "../shared"` — `modules.json` paths escape the sandbox root ("Module not installed"). Ubiquitous monorepo layout | **Accepted.** Discover computes the `..`-closure of local module sources and roots the sandbox there; copy cost stated honestly; reflink/overlay materialisation planned for large closures |
| M7 | MAJ | Run-block-level selection is achievable now: split test files one-run-per-file into the sandbox. Verified, including the `modules.json` run-key trap (synthesise per-sandbox `modules.json`, share only `.terraform/providers`) and the dependency-closed-prefix constraint for `run.<name>` references | **Accepted.** Replaces the "one-concern-per-file layout" recommendation and Open Question 1 entirely. Combined with C1's two-phase execution, this is the primary viability lever on real modules |
| M8 | MAJ | `Killed` conflates assertion kills with Terraform plan-error kills; an assertion-less suite kills mutants. Inflates scores; makes characterisation's until-dry oracle flattering from iteration zero | **Accepted with modification.** States split into `Killed` (assertion) and `KilledByError` (validate-clean, plan/apply error). Both remain in the headline score — a runtime error is a legitimate detection, consistent with field convention — but the split is always reported, and the characterisation loops (`--until-dry`, `curate`) count assertion kills only |
| M9 | MAJ | Mutant-count estimates 3–8× low (terraform-aws-vpc first 500 lines: 213 attribute sites, 118 string literals, 51 conditionals) | **Accepted.** Catalogue estimates and duration table re-baselined |
| M10 | MAJ | `--until-dry` output is a near-antichain under kill-set inclusion, so `curate`'s set-cover prunes almost nothing from it; and until-dry drives toward `configured`-level pinning regardless of the chosen ladder rung | **Accepted.** `--until-dry` now respects the granularity ladder (pins only deltas at or below the chosen level); `curate` re-scoped to externally-authored assertions and cross-scenario redundancy |
| M11 | MAJ | `validate` is not cheap with real providers (1.72 s vs 1.36 s for the test itself) — the pre-filter more than doubles per-mutant cost for the valid majority | **Accepted.** `validate` now runs only for operators with plausible static-failure modes; all other mutants classify `Invalid` vs `KilledByError` post hoc from the diagnostics in the `test -json` stream |
| M12 | MAJ | `VAR-VALIDATION-WEAKEN` as `condition = true` is 100% Invalid (condition must reference the variable); `VAR-TYPE-LOOSEN`'s stated killer is impossible (`expect_failures` cannot capture type-conversion errors; any killing test reddens the baseline) | **Accepted.** WEAKEN restated as `condition = can(var.<name>)`; TYPE-LOOSEN deleted from the catalogue with the rationale recorded |
| M13 | MAJ | Metric formulas don't cover the full state set (`MockMasked`, `Timeout` in neither formula); `Timeout ⇒ killed` plus a 10× factor over a 0.167 s baseline inflates scores under load | **Accepted.** Formulas restated over the full state set; timeout becomes `max(factor × baseline, 30 s)` and `Timeout` is its own reported state, not a kill |
| m14 | MIN | Symlink strategy chosen over portable `TF_DATA_DIR` without reason (Windows symlink privilege); `.terraform.lock.hcl` omission silently classifies the whole population Invalid; executed-test count must be a per-mutant invariant, not baseline-only | **Accepted.** Sandbox spec: per-sandbox `.terraform` directory containing synthesised `modules.json`, sharing `providers/` via link (junction on Windows) with `TF_DATA_DIR` fallback; lock file always copied; non-zero-executed-runs asserted per mutant |
| m15 | MIN | Asymmetric compatibility posture: `terraform graph` rejected for unstable output while the oracle rests on unpinned `plan_format_version` payloads; OpenTofu parity treated as a flag despite existing `test` divergence | **Accepted.** Accepted `plan_format_version` range pinned with loud failure outside it; OpenTofu is a tested matrix, not a flag |
| m16 | MIN | Suggested assertions are a thin moat (mechanical once the M2 plan delta exists — an agent could write them from the delta unaided); MVP definition-of-done sits behind the two latest milestones while C1 makes M3's speed features load-bearing | **Accepted with modification.** Moat reframed: the defensible layer is the verified classification/diagnosis loop plus the substrate quality, with suggestion generation as its cheapest consumer. Speed features (two-phase, run-block splitting) pulled into M2. The skills gate stays — it is a deliberate product commitment — but no longer trails the speed work it depends on |
| m17 | MIN | Validation-mining yield unquantified; the worked example (`contains` over a literal list) is the easiest case and also the fixture's own validation | **Accepted.** A corpus measurement of minable-validation share added as the explicit pre-M4.5 de-risking experiment |

## Corrections to the research record

Three statements in `docs/research/` were wrong or dangerously under-qualified and have been
amended in place, with pointers back to this review:

1. `01-terraform-test-capabilities.md` and `04-harness-spike.md` described
   `relevant_attributes` as "a coverage signal". It is the refresh/targeting dependency set
   (C2).
2. `04-harness-spike.md`'s throughput summary now carries the provider-schema scaling caveat
   (C1): the 43 mutants/s figure is real but specific to a 3.4 KB-schema provider, and
   `-verbose` cost scales with schema size per run block.
3. `04-harness-spike.md`'s child-module finding now notes it covers *downward* sources only;
   upward (`../`) sources escape the sandbox root (M6).

## Process note

The four critical findings share one cause: every load-bearing measurement was taken against
fixtures whose provider schema is ~3 KB. The review's own method — re-run the spike suite
against a fully-mocked real provider — is adopted as a standing rule: **no performance or
feasibility claim enters a design document until it has been measured against a
realistically-sized provider schema.** The M1 exit gate encodes this.
