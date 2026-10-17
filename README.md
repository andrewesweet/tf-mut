# tf-mut

Mutation testing for `terraform test`, designed for fully-mocked unit tests.

tf-mut is for a Terraform module author with a `terraform test` suite, or an agent acting for
one. A run scores the suite by what it does not check: every surviving mutant carries the
diagnosis of what would catch it.

You need Linux x86-64 (including WSL2), Terraform 1.15.8, Go 1.26.9 and mise 2026.10.7. Those
versions are pinned in `mise.toml`, and the CI workflow installs the same set, so the bootstrap
below reproduces the CI environment on your machine.

> `tf-mut run` and `tf-mut preview` drive Tiers 0–3
> against real Terraform, and every surviving mutant carries exactly one diagnosis: whether the
> fix is an assertion, a mock default, a different input, or acceptance. The oracle never claims
> an equality it cannot prove: an unknown value in the mutation's forward cone — judged under the
> fail-closed address adapters, with a whole-payload unknown check wherever a mapping
> fails — or volatility it could not decompose, makes the comparison indeterminate rather than
> identical.
> `tf-mut suggest` generates and *verifies* the assertions that kill the survivors, and
> `tf-mut characterise` points the same machinery the other way: it scaffolds, harvests, pins
> and verifies a first suite for a module that has none, deterministically and with no language
> model anywhere. `tf-mut todos` surfaces the judgement points it refuses to guess at,
> `--until-dry` closes the measured gap, and `tf-mut curate` reports redundancy from
> authoritative populations only. Both loops are drivable end to end from the shipped agent
> skills.

## Bootstrap and build

Run these steps from this checkout's root to install the tools, build tf-mut and run the
same quality checks as CI:

1. Trust the workspace configuration:

   ```bash
   mise trust
   ```

   Expected result: mise accepts this checkout's configuration, so later commands run without
   an untrusted-config prompt.

2. Install the exact locked tools:

   ```bash
   MISE_CONFIG_DIR="$PWD/.artifacts/mise-config" mise install --yes
   ```

   Expected result: mise reports all locked tools installed, including Terraform 1.15.8 and
   Go 1.26.9.

3. Install the source tools and the offline provider mirror:

   ```bash
   MISE_CONFIG_DIR="$PWD/.artifacts/mise-config" mise exec -- just tools-install
   ```

   Expected result: the provider mirror is populated and the step ends with
   `toolchain doctor: ok`.

4. Run the full reproducible gate:

   ```bash
   MISE_CONFIG_DIR="$PWD/.artifacts/mise-config" mise exec -- just ci
   ```

   Expected result: every recipe passes, matching the CI result for the same commit.

After bootstrap, Just is the shared local/CI control plane. `mise exec -- just --list`
documents focused format, lint, type, test, fuzz, mutation, and security recipes for Go,
Bash, JSON, YAML, TOML, Terraform, and GitHub Actions. `just security` is intentionally
separate from the reproducible `just ci` gate because vulnerability databases change over time.
CodeQL additionally analyses every language it supports here (Go and GitHub Actions) in
GitHub's code-scanning environment.

## Using it

```bash
tf-mut run     [flags] [PATH]   # mutate the module and report pseudo-tested resources
tf-mut preview [flags] [PATH]   # list the mutants as diffs, executing nothing
tf-mut suggest [flags] [PATH]   # generate and verify assertions for survivors
tf-mut characterise [flags] [PATH] # scaffold and verify a first suite
tf-mut todos   [flags] [PATH]   # list open characterisation judgement points
tf-mut curate  [flags] [PATH]   # report redundant assertions
tf-mut skill install [flags]     # install the shipped agent skills
tf-mut version                   # print the build version
```

`[PATH]` in the synopsis is the module directory: the brackets are not typed, and leaving the
argument out measures the current directory.

The build writes `.artifacts/bin/tf-mut`. Run the examples below from this checkout's root;
`mise exec` supplies the pinned Terraform and other tools.

The three commands a first run needs are `preview`, which lists the mutants as diffs without
executing anything; `run`, which mutates the module and reports what the suite does not check;
and `suggest`, which generates and verifies the assertions that kill the survivors:

```bash
mise exec -- ./.artifacts/bin/tf-mut preview ./module
mise exec -- ./.artifacts/bin/tf-mut run ./module
mise exec -- ./.artifacts/bin/tf-mut suggest ./module
```

Each command documents its own flags with defaults:
`mise exec -- ./.artifacts/bin/tf-mut <command> -h` prints them. Start with
`mise exec -- ./.artifacts/bin/tf-mut run -h`. The one flag the rest of this page assumes is
the output format:

| Flag | Purpose |
| --- | --- |
| `--reporter terminal\|json\|sarif\|mte\|html\|junit\|markdown` | Output format (`mte` is Mutation Testing Elements) |

Parallelism needs no flag on a first run: `--jobs` defaults to three-quarters of CPUs, min 1.

For suites under `tests/unit`, pass `--test-directory tests/unit` so Terraform installs
the modules called by those tests. Mocked suites can run in roots with a `backend` block,
including a remote backend, without initialising that backend. An effective `cloud`
declaration still causes provider schema retrieval to fail.

Each Terraform command runs in its own process group. After the command exits, tf-mut sends
`SIGKILL` to any processes left in that group, including provider plugins. Cleanup is best
effort; an already-exited group is ignored. Deadlines and evaluation interrupts also stop
the group. See [mutant states](docs/design/product-design.md#4-mutant-states) for how a test
deadline appears beside the mutant's verdict. The `--preflight-timeout` cap applies to
each call separately, including validation during mutant execution. The defaults are
10 minutes for `init` and 1 minute for other preflight calls, baseline runs and
characterisation's staged runs. Zero retains the defaults; negative durations are refused.
For a persistent cap,
see [configuration](docs/design/product-design.md#configuration--tf-muthcl).
Git calls, including checkout-root discovery and `--since`, have a fixed 30-second cap;
an overrun is an operational failure.

Survivor-side fingerprint, volatility and suggestion-verification runs use the mutant budget.
Fingerprint and volatility overruns produce a `Timeout` verdict counted in the score's
denominator. A suggestion-verification
overrun returns a fail-closed verification error: batch verification aborts, while an
isolated overrun records that member's execution failure. Other members still run, and a
completed kill can verify the suggestion. It does not change the mutant verdict or score.

An evaluation interrupted with Ctrl-C or SIGTERM returns an operational failure (exit code `2`).

Preview records safety-gate advisories and refusals in the report's warnings while still
listing the mutant population. It executes no test run blocks. For run blocks targeting
local modules, see the [checkout boundary](docs/design/product-design.md#discover).

For supported suggestion forms and skip rules, see the
[suggestion contract](docs/design/product-design.md#7-the-differentiator--suggested-assertions).

For verdict-cache storage, reuse and upgrades, see the
[cache contract](docs/design/product-design.md#10-ci-integration).

With `--reporter json`, `run`, `preview`, `suggest` and `curate` include performance telemetry.
The emitted contract is linked in the [document map](#documents). Every run carries one
identifier — the run span's trace ID when tracing is enabled, a random identifier of the same
32-hex-character shape otherwise — published as the report telemetry's `run_id` and prefixed
onto every stderr line the run writes (`[run_id] …`), so stderr, report and trace tie
together; each mutant's execution error is also recorded as an event on that mutant's span.
Mutants evaluated in this invocation carry phase timings and child-process CPU and peak memory
measurements. Cached, static and unexecuted verdicts carry no per-mutant telemetry. These
measurements do not affect verdicts, scores or gates.

Every ten seconds during mutant execution, the CLI writes completed/total mutants, elapsed
time and an estimated time remaining (ETA) to stderr, prefixed with the run identifier.
Counts exclude cached and static verdicts. A final line follows if periodic output began.
Shorter runs emit no progress lines, and progress works with every reporter.

When `--otlp-endpoint` is unset, tf-mut uses `OTEL_EXPORTER_OTLP_ENDPOINT`. With neither
configured, it sends no traces. Phase spans carry the recorded child CPU and peak memory
measurements. Endpoints use HTTP or HTTPS. tf-mut appends `/v1/traces` to
the supplied base path unless it already ends with that suffix. An unusable endpoint disables
tracing with a warning. Tracing warnings share the CLI's progress stream on stderr and never
enter report output. Export failures produce a fixed warning without endpoint paths or
credentials. Resource or sampler initialisation diagnostics produce a fixed warning while
preserving the OpenTelemetry SDK's fallback behaviour.

Use a new file outside the module directory for `--cpu-profile`. Its parent directory must
exist. tf-mut refuses existing files and symlinks, and warns on stderr if profile output fails
after profiling starts. For example, with `module` under this checkout's root:

```bash
mise exec -- ./.artifacts/bin/tf-mut run --no-cache --reporter json \
  --cpu-profile cpu.pprof \
  --otlp-endpoint http://localhost:4318 ./module > report.json
```

Exit codes: `0` ran clean, `1` findings (survivors, or a score below `--min-score`), `2`
operational failure. The source tree of the module under test is never written to, except for
the five recorded tool-owned write exceptions listed in the [AGENTS.md conventions](AGENTS.md#conventions).

## The problem

A green `terraform test` run tells you your configuration plans successfully. It does not tell
you your tests would notice if the configuration were wrong.

The first published measurement of this — Oasis's benchmark across 23 public Terraform
repositories — found a **24.9% mutation score**, with **78% of surviving mutants never covered
by any test at all**. tf-mut's own benchmark over the same 23 pinned repositories is published,
on its own axes and under stated limitations, in
[`docs/research/21-m5-benchmark.md`](docs/research/21-m5-benchmark.md). Terraform has no native
coverage capability and no accepted proposal to ship one
([hashicorp/terraform#37605](https://github.com/hashicorp/terraform/issues/37605), open since
September 2025).

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

One row per `docs/` directory, so the map holds as files are added. For the bounded
contexts and glossary, start with [`CONTEXT.md`](CONTEXT.md).

| Directory | Contents |
| --- | --- |
| [`docs/adr/`](docs/adr/) | Numbered architectural decision records, including superseded decisions |
| [`docs/agents/`](docs/agents/) | Agent operating notes: the issue tracker, triage labels, and domain pointers |
| [`docs/design/`](docs/design/) | Architecture, the operator catalogue, characterisation, agent integration, and pack design — start with [`product-design.md`](docs/design/product-design.md) |
| [`docs/reference/`](docs/reference/) | Generated command-line reference — [`cli.md`](docs/reference/cli.md) renders the same help table as `tf-mut --help` |
| [`docs/research/`](docs/research/) | Numbered research notes, measurements, and milestone exit gates |
| [`docs/reviews/`](docs/reviews/) | Adversarial and implementation reviews with their dispositions, which outrank design prose on conflict |
| [`docs/schema/`](docs/schema/) | Versioned report schemas and the vendored external schemas that outputs are validated against — the `json` reporter emits [`report-2.14.0.json`](docs/schema/report-2.14.0.json) |

## Findings that shaped the design

Established by running Terraform v1.15.8 locally, not by reading documentation. Full detail and
reproduction steps in [`docs/research/04-harness-spike.md`](docs/research/04-harness-spike.md).

| Finding | Consequence |
| --- | --- |
| `terraform init` costs 5.2 s; a mocked test run costs 0.167 s | Init once, then follow the [sandbox sharing rules](docs/design/product-design.md#execute) |
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
- Isolated [sandboxes](docs/design/product-design.md#execute). The source tree is never
  written to, so there is no git dependency and no drift on crash.
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
docs/adr/            Numbered architectural decision records, including superseded decisions
docs/agents/         Agent operating notes: the issue tracker, triage labels, and domain pointers
docs/design/         Product design and operator catalogue
docs/research/       Research notes, all claims sourced or verified
docs/reviews/        Adversarial and implementation reviews with their dispositions
docs/schema/         Versioned report schemas and the vendored external schemas
research/spikes/     Terraform fixtures used to verify the harness design
tests/               Agent and build-chain shell checks
cmd/tf-mut/          Command-line entry point, a thin shell over the engine
internal/engine/     The one testing seam: configuration in, report out
internal/discovery/  Module and test-file parsing, closure, safety detection
internal/mutation/   The operator catalogue, its applicability matrix, and generation
internal/fingerprint/  Canonical payloads, the volatile mask, and the masked delta
internal/oracle/     The mutant outcome, its diagnoses and their evidence
internal/suggest/    Suggestion candidates, verification and run-block patches
internal/characterise/  Scaffold planning, input synthesis and judgement points
internal/skill/      The shipped agent skills and the skill install protocol
internal/config/     .tf-mut.hcl and the inline suppression directives
internal/sandbox/    Sandbox materialisation, sharing rules, fresh-inode writes
internal/tfexec/     The Terraform CLI and its machine-readable output
internal/report/     The report value and its renderings
internal/tracing/    Run, mutant and phase spans over OTLP/HTTP
internal/buildinfo/  Build metadata injected by the linker
internal/buildchain/  Checks and update helpers for the shared build chain
internal/testsupport/  File-writing helpers shared by the test suites
scripts/             Private helpers behind the Just control plane
tools/               Source-tool and Terraform-provider lock domains
```

See [Reporters](docs/design/product-design.md#9-reporters) for the rendering contracts.

## Licence

No licence is specified.
