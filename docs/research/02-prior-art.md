# Prior art

## 1. General-purpose mutation testing

The field is mature; the vocabulary and the engineering problems are settled. What follows is
the subset that transfers to Terraform.

### Established tools

| Tool | Language | Notable mechanics |
| --- | --- | --- |
| PIT / pitest | Java | Bytecode mutation; the first generally-available *incremental* mutation testing system; coverage-driven test selection |
| Stryker (JS/.NET/Scala) | Multiple | Mutant states model, incremental mode via git diff matching, HTML report, `--since` diff filtering |
| mutmut | Python | Simple operator set, cached results keyed by source hash |
| cargo-mutants | Rust | Copies the source tree per mutant into a build directory; timeouts derived from a baseline run |
| Descartes | Java (PIT engine) | *Extreme* mutation: replace whole method bodies rather than instructions |

### Concepts worth importing wholesale

**Mutant states.** Stryker's model is the de facto standard and is what report consumers
expect: `Killed`, `Survived`, `NoCoverage`, `Timeout`, `CompileError`, `RuntimeError`,
`Ignored`. A Terraform tool needs the same skeleton plus domain-specific additions.

**Mutation score.** Killed ÷ (killed + survived), usually reported twice — once including
uncovered mutants in the denominator (`mutation score`) and once excluding them (`mutation
score covered`). Reporting only one number hides whether a low score is a coverage problem or
an assertion problem.

**Equivalent mutants.** Mutants that change the source but cannot change observable
behaviour. Detecting them is undecidable in general, so mainstream tools ask teams to
document and suppress them by hand. This is the single biggest usability tax in mutation
testing — and, as `04-harness-spike.md` shows, Terraform gives us an unusually good partial
oracle for free.

**Incremental / diff mode.** Both PIT and Stryker cache prior results and re-run only mutants
whose source or tests changed. Stryker additionally supports `--since <ref>` to mutate only
lines touched by a branch, which is what makes mutation testing tolerable as a PR gate.

**Timeouts.** A mutant that hangs is normally counted as killed, on the theory that an
infinite loop is a detectable behaviour change. Terraform's failure modes differ — a mutant
is far more likely to *error* than to hang — so the timeout rule needs restating rather than
copying.

**Extreme mutation (Descartes).** Instead of mutating individual instructions, delete whole
method bodies and see if anything notices. It generates an order of magnitude fewer mutants,
runs much faster, and surfaces *pseudo-tested* methods — code that is covered by tests but
whose behaviour nothing actually asserts on. Across 19 open-source projects the median
proportion of pseudo-tested methods was ~10%.

This is the most transferable idea in the whole field for Terraform, because Terraform
configuration is overwhelmingly *declarative attribute assignment*. The Terraform analogue of
"delete the method body" is "delete the attribute assignment" or "delete the resource" — and
the analogue of a pseudo-tested method is a **pseudo-tested resource**: one that a test plan
covers, but that no assertion inspects.

## 2. Terraform-specific prior art

### Oasis — the only existing Terraform mutation testing tool

[`DegenerateUSER/Oasis`](https://github.com/DegenerateUSER/Oasis) (MIT, Python, created 2 July
2026, last pushed 7 July 2026, 7 stars). A genuine, well-documented mutation testing framework
for Terraform and OpenTofu. It deserves credit for being first and for publishing an honest
empirical evaluation; the analysis below is about design space, not quality.

**What it does**

- 12 mutation operators across four categories: `security`, `resource`, `compliance` (tags),
  `variables`.
- Operators are matched by *attribute-name synonym sets scoped to resource types* — e.g.
  `storage_encrypted` only mutates on `aws_db_instance`, `aws_rds_cluster`, and similar, so an
  unrelated attribute called `encrypted` is not mutated as if it were storage encryption.
- Three mutation surfaces: resource body (`R`), `variable` default (`V`), `locals` /
  tag maps (`L`).
- Mutates in place in the git working tree and restores with `git checkout` via a `restored`
  context manager.
- Classifies each surviving mutant as `no_coverage`, `plan_only_no_assert`, or
  `weak_assertion`.
- CSV report; `preview` subcommand renders mutants as unified diffs without touching files.
- Self-updating native binaries; `--test-cmd "tofu test"` for OpenTofu.

**Its published benchmark** — 23 public Terraform repositories:

| Metric | Value |
| --- | --- |
| Mutants generated | 242 |
| Scored (valid, ran to completion) | 177 |
| Killed | 44 |
| Survived | 133 |
| **Mutation score** | **24.9%** |
| Unscorable (baseline needed cloud credentials) | 61 |

Of the 133 survivors, the classifier attributed **104 (78%) to `no_coverage`** and 29 (22%) to
`weak_assertion`.

**The three findings that should shape any successor tool**

1. **A 24.9% mutation score across real repositories.** Public Terraform test suites largely
   assert that an apply succeeds, not that specific attribute values are correct. There is a
   large, real quality gap to address.
2. **78% of survivors were never covered at all.** Coverage, not assertion strength, is the
   dominant failure mode. A tool that leads with a coverage answer is more useful than one
   that leads with a mutation score.
3. **61 of 242 mutants (25%) could not be scored because the test suite required live cloud
   credentials.** This is the strongest possible argument for the brief this project was given:
   optimise for the fully-mocked, plan-mode unit test, where none of that applies.

**Design decisions worth diverging from**

| Oasis | Consequence | Alternative |
| --- | --- | --- |
| Mutation by regex text edit over a brace-aware raw scan (`parser.py` states this explicitly) | Cannot reliably target expressions, only attribute assignments. No conditional, boolean, comparison, arithmetic, `for_each`, or `for`-expression operators are possible. | Real HCL AST via `hclwrite` |
| `python-hcl2` rejected as a validity gate because its grammar is stricter than Terraform's | Correct diagnosis of a real problem — but the fix chosen was to drop AST parsing entirely rather than to use HashiCorp's own parser | Use `hashicorp/hcl/v2`, the same library Terraform itself uses; the grammar gap disappears |
| Operators are domain/policy faults (S3 ACLs, encryption flags, instance sizing, tags) | 3 of 12 operators had **no target at all** in the real corpus (`FIXTURE`-validated only). Domain operators are provider- and resource-specific, so coverage of any given module is patchy. | Language-level operators fire on *any* module; keep domain operators as opt-in packs layered on top |
| In-place mutation of the git worktree, restored with `git checkout` | Forces serial execution, requires a git repo, and any crash outside the context manager risks leaving mutated source behind | Copy-on-write sandbox directories; the source tree is never written to |
| No equivalent-mutant detection | Unobservable mutants are reported as survivors, depressing the score and generating false work | Plan-fingerprint oracle (see `04-harness-spike.md`) |
| No incremental or diff mode | Whole-repo runs only; impractical as a PR gate | Cache keyed by (file hash, operator, site); `--since <ref>` |
| CSV output only | No PR annotation path | SARIF + JUnit + JSON + HTML |

### Adjacent Terraform testing tools (not mutation testing)

- **Terratest** (Gruntwork, Go) — deploys real infrastructure, asserts, destroys. Maximum
  flexibility, real cost, real credentials. Its own coverage issue
  ([gruntwork-io/terratest#556](https://github.com/gruntwork-io/terratest/issues/556)) has sat
  open for years.
- **`tftest`** — at least four unrelated projects share this name (a Python plan/apply helper
  on PyPI, and JavaScript assertion runners from getndazn, conde-nast-international and
  octocraft). None do mutation testing. **The name is thoroughly taken**, which is a point in
  favour of `tf-mut`.
- **Policy-as-code** — Checkov, tfsec/Trivy, OPA/conftest, Sentinel. These assert *rules about*
  configuration. They are complementary: mutation testing measures whether your tests would
  notice a fault, policy scanning asserts a fault class is absent. A mutation tool can borrow
  their rule catalogues as a source of realistic domain mutations.
- **Coverage** — no native support (`hashicorp/terraform#37605`, open since September 2025);
  community practice is ad hoc scripting that greps `.tftest.hcl` files for resource names.

## 3. Position

There is exactly one incumbent, it is four weeks old at time of writing, it is built on regex
text editing, and its own published data says the dominant problem is *coverage* rather than
assertion strength. The unoccupied ground is:

- a real-AST mutation engine using HashiCorp's own HCL library;
- a language-level operator catalogue that applies to every module rather than to specific AWS
  resource types;
- a design centred on **fully-mocked, credential-free unit tests**, which is precisely the
  quarter of the corpus Oasis could not score;
- equivalence detection and coverage reporting derived from Terraform's own plan JSON.

## Sources

- [DegenerateUSER/Oasis](https://github.com/DegenerateUSER/Oasis) — README, `Docs/OPERATOR_SYNONYMS.md`, `src/tfmutate/parser.py`, `benchmarks/`
- [Stryker — mutant states and metrics](https://stryker-mutator.io/docs/mutation-testing-elements/mutant-states-and-metrics/)
- [Stryker — incremental mode](https://stryker-mutator.io/docs/stryker-js/incremental/)
- [Descartes: a PITest engine to detect pseudo-tested methods](https://inria.hal.science/hal-01870976/document)
- [A Comprehensive Study of Pseudo-tested Methods](https://arxiv.org/pdf/1807.05030)
- [Extreme mutation testing in practice: an industrial case study](https://arxiv.org/pdf/2103.08480)
- [Testing Practices for Infrastructure as Code](https://akondrahman.github.io/files/papers/langeti2020.pdf)
- [gruntwork-io/terratest#556 — measuring code coverage](https://github.com/gruntwork-io/terratest/issues/556)
