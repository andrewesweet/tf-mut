# M3 implementation review — 17 August 2026

What implementing milestone M3 ([issue #33](https://github.com/andrewesweet/tf-mut/issues/33)
and its sub-scope tickets #44–#54) taught, recorded the way M1's and M2's reviews were, so
that the M4 spec absorbs it rather than rediscovering it.

The same two kinds of entry, and the difference still matters: **measurements** are facts
with standing-rule-1 force; **decisions** are what the implementation did about them, and a
later spec revising one should say so deliberately. The recommendations are collected at the
end as **open questions**, each awaiting a disposition in either direction.

---

## Measurements

| # | Measurement | Consequence |
| --- | --- | --- |
| M3-A | **The pinned one-file diff runs in 40.1 s against a full run's 539.2 s — 13.5× — on the named hardware**, 219 standard-tier mutants, cache off, warm plugin cache (`../research/09-m3-real-provider-gate.md`; the first publication measured 51.9 s / 757.6 s / 14.6× and was re-measured after the delivery review's graph repair, on warmer OS caches — both runs improved, the factor barely moved). Full-run throughput confirms the ~1.6 s provider-startup floor for a third milestone running. | The count levers work, and the sub-minute inner-loop story holds for configuration-only diffs on this class of hardware, now behind a 4× portable factor floor. The narrowed claim stands: a code-plus-test PR forces the full population and is not sub-minute. |
| M3-B | **`mock-masked`'s positive case cannot fire.** Measured against `hashicorp/aws` v6.60.0 in apply mode: a stable delta in an optional-computed attribute is attributable to the module (the mock invents `0` deterministically for numbers, and an assertion could pin the configured value); a computed-only attribute's mock value is either deterministic and identical on both sides or a random string the volatility re-run masks. | The diagnosis was withdrawn, prove-or-withdraw executed as the spec's hard criterion demanded. The refutation is pinned by a network-gated test so the diagnosis cannot quietly return. `Schemas.Computed` survives — the volatility re-run rule still needs it. |
| M3-C | **The median real survivor delta is exactly three changes** (143 survivors on the post-repair re-run; 141 on the first publication); the 90th percentile is 18 and only whole-resource mutants saturate the JSON's cap of twenty (4.2%). | Both display bounds — three in the terminal, twenty in the JSON — now rest on measurement rather than a guess, answering M2 open question 5 without changing either number. |
| M3-D | **The generated function families produce zero invalid and zero error mutants** on the real module: five mutants over three candidate sites, every pair clean (`m3e-admission.json`). | The fault-model catalogue's admission evidence exists and is clean; admission to `standard` remains a deliberate, separate change. Contrast with C7's counterfactual: the signature-compatibility catalogue would have produced 525 pairs. |
| M3-E | **Git ignore rules — personal or repository-level — can hide changed files from selection**: a global ignore for `*.tfvars` made `ls-files --others` omit a variables file that forces the full population, and Terraform reads none of git's ignore rules. | The engine's git commands neutralise `GIT_CONFIG_GLOBAL`/`GIT_CONFIG_SYSTEM`, and — after the delivery review sharpened the point — ignored untracked files are enumerated explicitly, so the repository's `.gitignore` shapes execution selection no more than a personal one does. Standing rule 5's scope grew from subprocess environment to subprocess *configuration*. |
| M3-F | **`test_plan` carries only `resource_changes`, `output_changes` and the format versions** — no `variables`, no `configuration` member — and every `terraform_data` resource carries `id` and `output` unknowns in plan mode. Verified against the binary before the M3a fixtures were designed. | The static `Unobservable` shortcut is sound for unused locals and variables (they leave no payload trace), and the same-resource attribute union correctly keeps own-resource mutants indeterminate in plan mode. Standing rule 3 pays again. |

## Decisions

| # | Finding | Disposition |
| --- | --- | --- |
| M3-1 | C1's mandatory "a mutant inside the condition executes" had no generation site: Tier 1 expression operators never visited meta-argument attributes. | **The conditional, boolean and comparison operators now fire inside `count` and `for_each` expressions**; literals and every other shape stay with Tier 2, and the matrix records the split. The alternative — a vacuously-true mandatory case — is the kind of green the review pattern note warns about. |
| M3-2 | The upstream-of-multiplicity question cannot use the forward cone: the cone's block-union edges make every body attribute "upstream" of its own resource's count. | **Upstream is answered from pure reference edges** (`Graph.MultiplicityGuard`), kept separately from the cone's dependents. Two graph relations, each conservative for its own consumer. |
| M3-3 | The M6 "project-local default location" collides with the never-write contract. | **The cache became the contract's first recorded exception** — `.tf-mut-cache/` is tool-owned, excluded from sandbox copies, and the tree-digest assertion carves it out by name — and the baseline file later joined it as the second, written only on an explicit `--write-baseline`. Module sources are still never written. |
| M3-4 | Statically classified states in a preview would break the documented "every preview mutant is Pending" contract. | **The static shortcuts fire only where execution would otherwise run.** A preview stays a population listing. |
| M3-5 | A run-level variable assignment that is not a context-free literal must not fall through to the default it overrides — the first evaluator draft did exactly that and mis-decided the run.* fixture. | **The first level that assigns a name decides**; an unresolvable assignment fails the evaluation closed. |
| M3-6 | Sampling, `--fail-on-new` and `--min-score` compose; a sampled gate is refusable at configuration time. | **Gate refusals happen in `finalise`, before any Terraform runs** — the same posture as the safety gates: a misconfiguration must not cost a baseline run to discover. |
| M3-7 | The MTE viewer bundle contains a documentation hyperlink, which a naive self-containment check flags. | **Self-contained means no resource loads** — script/img src, link href, CSS imports — and the check targets exactly those. A navigation anchor renders offline. |
| M3-8 | `Timeout` verdicts could be cached. | **They are not**: a budget overrun is not a fact about the module. The statically classified states are also uncached — recomputing them is cheaper than reading them. |
| M3-9 | The Action needs testability without weakening the distribution contract. | **`download-base-url` parameterises the asset host, never the verification**: the workflow test serves a locally built release over `file://` and the checksum gate applies to it identically. |

## The adversarial delivery review, and what its repairs changed

The milestone was rejected once and repaired (the record is on issues #44–#54 and in the
exit-gate document's correction list). Beyond the itemised repairs, three entries above were
touched: M3-A's measurements were refreshed by the post-repair re-run, M3-E's `.gitignore`
disposition was inverted by the review's sharper reading of the C5 clause, and the
`mock-masked` withdrawal gained a schema-compatibility clause — the 2.1.0 vocabulary retains
the withdrawn value as deprecated so the revision stays additive.

## What this milestone's process caught

- The adapter sweep over every generation site (C3's own requirement) caught the two
  block-removal operators whose sites name nested blocks rather than attributes — exactly
  the drift the sweep exists for, on its first run.
- The `TestSourceTreeIsUntouchedByEveryRun` harness caught the cache writing into the tree
  before any human review would have (M3-3's deviation was then taken openly).
- The families fixture's `file()` bait was worth its two lines: the cross-family assertion
  is a real check, not a tautology, because the bait shares the unary string signature C7
  named.

## Open questions for the M4 spec

1. **Should the generated families be admitted to `standard`?** The evidence is published
   and clean (M3-D), but it is five mutants over three sites on one module. The admission
   gate's own standard — "published counts on a real module" — is met to the letter;
   whether the sample is persuasive is a judgement the spec author should make explicitly.
2. **Does the cache deserve a finer key?** The coarse key invalidates the whole population
   on any closure change (a child-module touch invalidates root verdicts — deliberately,
   per C4). Graph-derived invalidation "returns only with a measurement proving the coarse
   key insufficient": nobody has yet measured how often real edits invalidate more than the
   graph would. That measurement is cheap now that the cache and graph both exist.
3. **Should the evaluator's supported forms grow?** `local.x` references in multiplicity
   expressions fail closed to execution today. The enumerated-context design makes adding
   locals a bounded change; whether real modules gate instantiation through locals often
   enough to matter is unmeasured.
4. **Is the Action's comment worth its write permission?** The SARIF annotations carry the
   same findings into the PR view with `security-events` alone. If real adoption shows the
   comment redundant, dropping it halves the requested permissions.
5. **The workflow-level Action test has not yet run on GitHub's runners** — it ships in this
   milestone and executes on the next push. If it needs adjustment, that is CI-shape
   debugging, not a contract change; the contract's bash is proven locally.
