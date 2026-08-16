# tf-mut

Mutation testing for `terraform test`, designed for fully-mocked unit tests.

> **Status: M2 complete — the honesty milestone.** `tf-mut run` and `tf-mut preview` drive
> Tiers 0–3 against real Terraform, and every surviving mutant carries exactly one diagnosis:
> whether the fix is an assertion, a mock default, a different input, or acceptance. The
> fingerprint oracle behind that answer refuses the comfortable one — an unknown value anywhere
> in the payload, or volatility it could not decompose, makes the comparison indeterminate
> rather than identical. Configuration, reasoned suppression and SARIF ship with it. Suggested
> assertions, the incremental speed levers and characterisation mode are later milestones.

## Bootstrap and build

The initial supported platform is Linux x86-64, including WSL2. Install the mise version pinned
in `mise.toml`, then run:

```bash
mise trust
MISE_CONFIG_DIR="$PWD/.artifacts/mise-config" mise install --yes
MISE_CONFIG_DIR="$PWD/.artifacts/mise-config" mise exec -- just tools-install
MISE_CONFIG_DIR="$PWD/.artifacts/mise-config" mise exec -- just ci
```

After bootstrap, Just is the shared local/CI control plane. `just --list` documents focused
format, lint, type, test, fuzz, mutation, and security recipes for Go, Bash, JSON, YAML, TOML,
Terraform, and GitHub Actions. `just security` is intentionally separate from the reproducible
`just ci` gate because vulnerability databases change over time. CodeQL additionally analyzes
every language it supports here (Go and GitHub Actions) in GitHub's code-scanning environment.

## Using it

```bash
tf-mut run     [flags] [PATH]   # mutate the module and report pseudo-tested resources
tf-mut preview [flags] [PATH]   # list the mutants as diffs, executing nothing
```

| Flag | Purpose |
| --- | --- |
| `--test-directory PATH` | Test directory relative to the module (default `tests`) |
| `--jobs N` | Mutants executing concurrently (default: CPU count) |
| `--timeout-factor F` | Multiple of the baseline run time; the budget is `max(F × baseline, 30 s)` |
| `--min-score N` | Fail below this mutation score percentage |
| `--allow-incomplete-score` | Let a timeout-affected score satisfy `--min-score` |
| `--allow-real-infrastructure` | Required when any provider is unmocked |
| `--allow-unsandboxed-effects` | Required for apply-mode provisioners and unsevered data sources |
| `--reporter terminal\|json` | Output format |

Exit codes: `0` ran clean, `1` findings (survivors, or a score below `--min-score`), `2`
operational failure. The source tree of the module under test is never written to.

## The problem

A green `terraform test` run tells you your configuration plans successfully. It does not tell
you your tests would notice if the configuration were wrong.

The only published measurement of this — Oasis's benchmark across 23 public Terraform
repositories — found a **24.9% mutation score**, with **78% of surviving mutants never covered
by any test at all**. Terraform has no native coverage capability and no accepted proposal to
ship one ([hashicorp/terraform#37605](https://github.com/hashicorp/terraform/issues/37605),
open since September 2025).

## The approach

Inject small, realistic faults into the configuration, run the test suite against each, and
report which faults the tests failed to catch — then generate the assertion that would have
caught them.

The design targets unit tests where all provider behaviour is mocked: no credentials, no
cloud cost. Throughput depends dominantly on provider schema size — measured at ~43 mutants/s
(8 cores) with a small-schema provider, and orders of magnitude less naively against
`hashicorp/aws`, whose 14.5 MB schema is re-serialised into every verbose test message. The
design's two-phase execution and run-block-level test selection exist to make the inner loop
(`--since`, smoke tier) interactive on real modules regardless; full sweeps are scheduled
work. An independent adversarial review drove these corrections — see
[`docs/reviews/2026-08-15-adversarial-review.md`](docs/reviews/2026-08-15-adversarial-review.md).

## Documents

| Document | Contents |
| --- | --- |
| [`docs/design/product-design.md`](docs/design/product-design.md) | **Start here.** Proposed product capabilities, architecture, metrics, CLI, roadmap |
| [`docs/design/mutation-operators.md`](docs/design/mutation-operators.md) | The operator catalogue, in six tiers |
| [`docs/design/characterisation.md`](docs/design/characterisation.md) | Characterisation mode — scaffolding unit tests for legacy modules, no LLM required |
| [`docs/design/agent-integration.md`](docs/design/agent-integration.md) | Failure-mode taxonomy, and driving the tool from a coding agent |
| [`docs/reviews/2026-08-15-adversarial-review.md`](docs/reviews/2026-08-15-adversarial-review.md) | Adversarial review round 1 (4 critical) and dispositions |
| [`docs/reviews/2026-08-16-adversarial-review-r2.md`](docs/reviews/2026-08-16-adversarial-review-r2.md) | Round 2, external agent, over the round-1 revisions (5 critical) and dispositions |
| [`docs/reviews/2026-08-16-build-chain-implementation-review.md`](docs/reviews/2026-08-16-build-chain-implementation-review.md) | Sol/xhigh adversarial review of the implemented build chain and dispositions |
| [`docs/reviews/2026-08-16-m1-implementation-review.md`](docs/reviews/2026-08-16-m1-implementation-review.md) | What implementing M1 measured, decided and deferred, and the open questions M2 must dispose of |
| [`docs/reviews/2026-08-16-m2-spec-review.md`](docs/reviews/2026-08-16-m2-spec-review.md) | Adversarial review of the M2 spec, and the ten dispositions that shaped it |
| [`docs/reviews/2026-08-16-m2-implementation-review.md`](docs/reviews/2026-08-16-m2-implementation-review.md) | What implementing M2 measured, decided and deferred, and the open questions M3 must dispose of |
| [`docs/research/01-terraform-test-capabilities.md`](docs/research/01-terraform-test-capabilities.md) | What `terraform test` can do in v1.15.8, verified against the CLI |
| [`docs/research/02-prior-art.md`](docs/research/02-prior-art.md) | Mutation testing prior art, and an analysis of Oasis |
| [`docs/research/03-hcl2-tooling.md`](docs/research/03-hcl2-tooling.md) | HCL2 tooling and the mutable surface of the language |
| [`docs/research/04-harness-spike.md`](docs/research/04-harness-spike.md) | Measured spike results — isolation, equivalence, throughput |
| [`docs/research/05-go-build-chain.md`](docs/research/05-go-build-chain.md) | Accepted Go/Just/mise build-chain contract and implementation notes |
| [`docs/research/06-m1-exit-gate.md`](docs/research/06-m1-exit-gate.md) | M1 exit gates: the reproduction map, the deviations, and the measured real-provider numbers |
| [`docs/research/07-m2-cost-model.md`](docs/research/07-m2-cost-model.md) | What M2's planned speed levers actually buy, measured — and why run-block splitting was dropped |
| [`docs/research/08-m2-exit-gate.md`](docs/research/08-m2-exit-gate.md) | The honesty gate, the contract sweep from every normative behaviour to its test, and M2's measurements |
| [`docs/schema/report-1.0.0.json`](docs/schema/report-1.0.0.json) | The M1 JSON report schema, still published for consumers that read it |
| [`docs/schema/report-2.0.0.json`](docs/schema/report-2.0.0.json) | The versioned JSON report schema the `json` reporter emits |
| [`docs/schema/sarif-2.1.0.json`](docs/schema/sarif-2.1.0.json) | The published SARIF schema the `sarif` reporter's output is validated against |

## Findings that shaped the design

Established by running Terraform v1.15.8 locally, not by reading documentation. Full detail and
reproduction steps in [`docs/research/04-harness-spike.md`](docs/research/04-harness-spike.md).

| Finding | Consequence |
| --- | --- |
| `terraform init` costs 5.2 s; a mocked test run costs 0.167 s | Init once, share `.terraform` read-only across mutant sandboxes |
| Local module paths in `.terraform/modules/modules.json` are **relative** | Provider sharing is safe and downward child-module mutations propagate; upward (`../`) sources require rooting the sandbox at their closure |
| 100 mutants, 8 workers, mocked plan tests, small-schema provider: **2.3 s** | The inner loop can be interactive — with the two-phase execution and test selection the design specifies for real-provider schemas |
| `-filter` is **file**-scoped, not run-scoped, and exits **0** when it matches nothing | Test selection is per-file; the executed-test count must be asserted explicitly |
| `terraform validate` separates static from dynamic failure | Clean rule: validate fails ⇒ invalid mutant, discard; validate passes but run errors ⇒ killed |
| Plan JSON fingerprints are stable under unobservable edits and move under behavioural ones | Automatic detection of mutants no assertion could possibly catch |
| Auto-generated mock values are **not deterministic** across runs | Volatile attributes are masked from every fingerprint via a static impure-function scan unioned with a two-run baseline diff |
| Mocks work with `command = apply`, contrary to widely-repeated documentation | Apply-mode mocked runs expose state and outputs, widening the killable surface |

## What would make it different

- A real HCL AST via `github.com/hashicorp/hcl/v2` — the parser Terraform itself uses — rather
  than regex text editing, which is what the only existing tool does and what caps its operator
  catalogue at attribute assignments.
- Language-level operators that fire on any module and any provider, rather than curated
  `(resource_type, attribute)` pairs that only match specific AWS resources.
- Copy-on-write sandboxes with a shared `.terraform`. The source tree is never written to, so
  there is no git dependency and no drift on crash.
- Automatic detection of mutants that *cannot* be observed, so the tool does not generate work
  that has no fix.
- Mock-aware diagnosis, so a mocked suite is not told to assert on values the mock invented.
- **Suggested assertions.** For every surviving mutant the tool holds the baseline plan and the
  mutant plan; the difference between them is the assertion that would have killed it. That is
  reliably possible in Terraform, and in almost no other language, because the observable state
  is a structured document with stable addresses.
- Coverage reporting as a by-product of the baseline, filling a documented three-year gap at no
  extra cost.

## Repository layout

```
docs/design/      Product design and operator catalogue
docs/research/    Research notes, all claims sourced or verified
research/spikes/  Terraform fixtures used to verify the harness design
cmd/tf-mut/       Command-line entry point, a thin shell over the engine
internal/engine/  The one testing seam: configuration in, report out
internal/discovery/  Module and test-file parsing, closure, safety detection
internal/mutation/   The operator catalogue, its applicability matrix, and generation
internal/fingerprint/  Canonical payloads, the volatile mask, and the masked delta
internal/config/     .tf-mut.hcl and the inline suppression directives
internal/sandbox/    Sandbox materialisation, sharing rules, fresh-inode writes
internal/tfexec/     The Terraform CLI and its machine-readable output
internal/report/     The report value, and the terminal, JSON and SARIF renderings
scripts/          Private helpers behind the Just control plane
tools/            Source-tool and Terraform-provider lock domains
```

## Licence

Not yet chosen.
