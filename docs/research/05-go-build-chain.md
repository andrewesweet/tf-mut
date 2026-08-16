# Initial Go build chain proposal

**Status:** accepted and implemented as the initial chain

**Research snapshot:** 2026-08-16

**Revision:** incorporates the accepted findings from the
[issue #3 adversarial review](https://github.com/andrewesweet/tf-mut/issues/3#issuecomment-5306503165).

## Decision

Use **Just as the only human- and CI-facing control plane** after two documented direct
pre-control-plane commands. Provisioning underneath it is deliberately hybrid because no single lock format
covers every executable this project needs with equal integrity:

- `mise.toml`/`mise.lock` manage Go and tools that publish verifiable release binaries;
- isolated `tools/<name>/go.mod`/`go.sum` modules manage source-only Go tools;
- root `go.mod`/`go.sum` manage product and test dependencies;
- committed fixture `.terraform.lock.hcl` files manage the provider binaries executed by the
  real-Terraform test seam;
- immutable GitHub Action SHAs are updated by Dependabot.

Mise entries may use fuzzy selectors such as `latest`, but the committed lock records the
exact reviewed version, URL and checksum through a full-support backend. Normal local and CI
installation consumes those locks; only explicit update recipes re-resolve them. The initial
supported development and CI platform is **Linux x86-64** (`linux-x64`/`linux_amd64`), including
WSL2. macOS, Linux arm64 and Windows require portability spikes before they enter the matrix.

This gives the project both meanings of “latest” that it needs:

- a reproducible, exact snapshot for every ordinary local and CI run;
- an explicit, reviewable command that moves that snapshot to the latest available releases.

CI must remain thin: bootstrap a pinned mise release, install the locked tools, then call Just
recipes. It must not grow a second set of raw Go, lint, test or security commands.

The initial quality posture should be deliberately strict:

- `goimports`, then `gofumpt -extra`, with separate check and write recipes;
- `shfmt`, native `bash -n` and ShellCheck for every Bash helper;
- canonical jq, yamlfmt and Tombi formatting plus parse/lint gates for JSON, YAML and TOML;
- actionlint and zizmor's aggressive auditor persona over workflows and Dependabot;
- golangci-lint v2 with `default: all`, explicit justified exceptions and a separate fix mode;
- compiler/type checks over normal and integration build tags;
- offline, real-Terraform tests as the normal test suite, with race, property and seed-corpus
  checks required on pull requests;
- fuzz seed-corpus checks and diff mutation on pull requests, bounded active fuzzing and full
  mutation sweeps on a schedule;
- govulncheck, golangci-lint's gosec analyzer, OSV-Scanner, Gitleaks, zizmor, CodeQL and
  module-integrity checks;
- one canonical `AGENTS.md`, symlinked as `CLAUDE.md`, plus shared skills and hook commands.

Implementation installed the exact project tools into mise's managed store and `.tools/bin`.
It also exposed a compatibility constraint missed by the proposal: gopls v0.23.0 does not
compile when Go module version selection raises `golang.org/x/tools` to v0.49.0 for goimports.
Each source-built command therefore has an isolated exact module and checksum set. This keeps
all three current instead of downgrading gopls or goimports.

## Repository-specific constraints

The repository now has a minimal Go module, version command, Justfile, tool manifests and CI
workflow. The product engine remains in its design phase; the minimal executable exists to make
the chain executable and mutation-test the cross-package test seam.

The fixed testing decision in `CLAUDE.md` materially changes the usual Go test taxonomy. The
authoritative seam is the engine entry point—configuration in, report out—using the **real
Terraform binary**. A fake Terraform runner would test the wrong thing. Accordingly:

- `just test` is an offline-after-bootstrap, millisecond-scale suite over `terraform_data` and
  mocked/null fixtures, but still invokes real Terraform;
- the R1/R2 reproductions remain mandatory fixtures;
- generated/property tests exercise externally visible behaviour through the same seam;
- networked real-provider and performance cases live behind a separate explicit recipe;
- neither ordinary hooks nor ordinary PR checks may accidentally contact real infrastructure.

“Offline” must be true by construction rather than dependent on a warm accidental cache. Every
fixture that uses a provider must pin an exact provider version and commit its lock file for
`linux_amd64`. The networked `tools-install` bootstrap populates a filesystem provider mirror;
normal tests set `TF_CLI_CONFIG_FILE` so Terraform can resolve only the allowlisted providers
from that mirror. A no-egress test is an acceptance gate. The generic `.terraform.lock.hcl`
ignore rule must therefore be removed; only generated `.terraform/` working directories remain
ignored.

This makes the default suite “unit-sized” in cost and isolation, but integration-shaped in its
use of the real executable. Naming must not tempt a later contributor to replace it with mocks.

## Current machine and proposed pins

The local audit found Go 1.26.3, mise 2026.5.3, Just 1.57.0, Terraform 1.15.8,
golangci-lint 2.12.2, gopls 0.21.1 and Gitleaks 8.30.1. The remaining proposed tools were not
installed. The initial locks should resolve to the following snapshot:

| Tool | Snapshot | Authority | Role |
| --- | --- | --- | --- |
| Go | `1.26.6` | `mise.lock` | compiler, tests, race and native fuzz driver |
| mise | `2026.8.6` | bootstrap pin | provisioning layer; cannot manage itself |
| Just | `1.58.0` | `mise.lock` | sole public task interface after bootstrap |
| Terraform | `1.15.8` | `mise.lock` | executable required by the fixed test seam |
| golangci-lint | `2.12.2` | `mise.lock` | lint, gosec and coordinated safe autofixes |
| gopls | `0.23.0` | `tools/gopls/go.sum` | language server for editors and Claude Code |
| gofumpt | `0.11.0` | `mise.lock` | stricter formatting than gofmt |
| goimports (`x/tools`) | `0.49.0` | `tools/goimports/go.sum` | imports before gofumpt |
| gotestsum | `1.13.0` | `mise.lock` | local/CI output and JUnit/JSON artifacts |
| govulncheck | `1.7.0` | `tools/govulncheck/go.sum` | Go reachability-aware vulnerability analysis |
| Gremlins | `0.6.0` | `mise.lock` | mutation testing, subject to compatibility spike |
| OSV-Scanner | `2.5.0` | `mise.lock` | ecosystem-wide dependency vulnerability scan |
| Gitleaks | `8.30.1` | `mise.lock` | current-tree and history secret detection |
| jq | `1.8.2` | `mise.lock` | JSON adapter and mutation-report assertions |
| shfmt | `3.13.1` | `mise.lock` | canonical Bash formatting |
| ShellCheck | `0.11.0` | `mise.lock` | Bash static analysis |
| yamlfmt | `0.21.0` | `mise.lock` | canonical YAML formatting and parse checks |
| Tombi | `1.4.0` | `mise.lock` | TOML formatting, parsing and linting |
| actionlint | `1.7.12` | `mise.lock` | GitHub Actions parsing and semantic linting |
| zizmor | `1.29.0` | `mise.lock` | GitHub Actions and Dependabot security analysis |
| Rapid | `1.3.0` | root `go.sum` | property/state-machine test dependency |

Go's live download feed identified 1.26.6 as the current stable release; it should replace the
machine's 1.26.3. Just 1.58.0 likewise replaces the installed 1.57.0. The table is dated
generated evidence for the first locks, not a permanent hand-maintained authority;
`just tools-outdated` becomes the authoritative report after implementation.

Primary version sources: [Go downloads](https://go.dev/dl/?mode=json),
[mise releases](https://github.com/jdx/mise/releases/latest),
[Just releases](https://github.com/casey/just/releases/latest),
[Terraform releases](https://releases.hashicorp.com/terraform/),
[golangci-lint releases](https://github.com/golangci/golangci-lint/releases/latest),
[gopls releases](https://go.dev/gopls/release/),
[gofumpt releases](https://github.com/mvdan/gofumpt/releases/latest),
[goimports package](https://pkg.go.dev/golang.org/x/tools/cmd/goimports),
[gotestsum releases](https://github.com/gotestyourself/gotestsum/releases/latest),
[govulncheck package](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck),
[Gremlins releases](https://github.com/go-gremlins/gremlins/releases/latest),
[OSV-Scanner releases](https://github.com/google/osv-scanner/releases/latest),
[Gitleaks releases](https://github.com/gitleaks/gitleaks/releases/latest),
[shfmt releases](https://github.com/mvdan/sh/releases/latest),
[yamlfmt releases](https://github.com/google/yamlfmt/releases/latest),
[Tombi releases](https://github.com/tombi-toml/tombi/releases/latest),
[actionlint releases](https://github.com/rhysd/actionlint/releases/latest),
[zizmor releases](https://github.com/woodruffw/zizmor/releases/latest) and
[Rapid](https://pkg.go.dev/pgregory.net/rapid).

### Lock domains and backend policy

Mise is used only where a full-support `aqua:` or `github:` backend supplies version, checksum,
size and URL. The first implementation must prove that every selected entry satisfies strict
locked installation on `linux-x64`; a backend that cannot do so blocks the rollout. Project
configuration uses `[tool_config] locked = true` so it does not accidentally impose lock policy
on a contributor's unrelated global mise tools. `mise lock --platform linux-x64` populates the
supported platform explicitly, and CI additionally sets `MISE_LOCKED=1` in its clean environment.
Where available, provenance is verified when generating the lock and reverified in CI with
`MISE_LOCKED_VERIFY_PROVENANCE=1`. See the
[mise backend matrix and strict mode](https://mise.jdx.dev/dev-tools/mise-lock.html#backend-support).

Gopls, goimports and govulncheck do not publish suitable release binaries. They belong in
isolated `tools/gopls`, `tools/goimports`, and `tools/govulncheck` modules with exact module
versions. Isolation is required because their latest dependency graphs are incompatible under
minimum version selection. Each `go.sum` authenticates its module content; `tools-install`
downloads and builds stable repo-local launchers under `.tools/bin`. Product and test
dependencies—including Rapid—remain in the root module. Official prebuilt golangci-lint is
retained because its project warns that source builds are not guaranteed to match release
binaries.

Provider binaries are a fourth lock domain. Every provider-bearing fixture uses an exact
constraint, a committed `.terraform.lock.hcl`, a committed allowlist and the bootstrap-populated
filesystem mirror. `just providers-update` is the only operation that changes provider versions
or hashes. The implementation should initially pin `hashicorp/null` exactly rather than the
current `~> 3.2` constraint.

Set `GOTOOLCHAIN=local` throughout mise and Just. The `go` and `toolchain` directives remain
requirements/documentation, but Go may not silently download a second, unlocked toolchain.
`doctor` compares the active `go version`, `go.mod` preference and `mise.lock` and fails on drift.

### Bootstrap and update policy

Bootstrap has exactly two direct commands before Just can become the interface, followed by one
Just recipe:

1. install/update mise itself to the separately pinned 2026.8.6 release;
2. run `mise install` directly, under project-scoped strict locking, to install Just and the
   other prebuilt tools;
3. use the now-installed Just to run `just tools-install`, download/build the isolated source
   tool modules and populate the exact Terraform provider mirror, then run `just doctor` and the normal
   gates.

Document this sequence in the README and canonical `AGENTS.md`. No recipe can install Just on
a machine that does not yet have Just.

The repository should then expose:

- `just tools-install` — consume all existing locks and populate `.tools`; never resolve newer
  versions;
- `just tools-outdated` — report updates across mise, the tools module and providers;
- `just tools-update` — safe-mode `mise upgrade --bump`, regenerate the `linux-x64` lock,
  update the three source Go tools, synchronize every Go module's language/toolchain directives,
  and run all gates;
- `just providers-update` — explicitly update provider constraints, platform hashes and mirror;
- `just deps-update` — update Go product/test dependencies separately and run the full gate;
- `just update` — deliberately perform all tool, provider and dependency update workflows for a
  scheduled update pull request.

CI must use locked mode and pin its mise bootstrap/action by immutable commit SHA. It must never
invoke `@latest`, `mise lock --bump`, or a floating action tag during a normal run. A weekly
scheduled job may run the update recipes in mise safe mode and open a reviewable pull request.
Dependabot owns action-SHA update pull requests. A release-age delay remains deferred because
the initial requirement is latest stable; adopting one later requires an explicit policy change.

The application module should start with a language floor and a patch-level preference, for
example `go 1.26.0` and `toolchain go1.26.6`. With `GOTOOLCHAIN=local`, a mismatch fails instead
of downloading an unreviewed toolchain. Go documents both behaviours in
[toolchain selection](https://go.dev/doc/toolchain).

## Just command contract

Recipe names are the stable interface. Their implementation may evolve without changing CI,
agent instructions or muscle memory.

| Recipe | Contract |
| --- | --- |
| `just doctor` | Fail on unsupported platform, lock/version drift, `GOTOOLCHAIN != local`, missing provider mirror, C compiler needed for race, invalid config or broken agent links. No writes. |
| `just tools-install` | Consume all committed locks and populate source-tool binaries and the provider mirror. May use the network; resolves no newer versions. |
| `just tools-outdated` | Report available mise, Go-tool, dependency and provider updates. No writes. |
| `just tools-update` | Resolve latest development tools, refresh Linux lock metadata/provenance, install and verify. Writes version metadata only. |
| `just providers-update` | Resolve reviewed provider updates and regenerate exact constraints, `linux_amd64` hashes and the mirror. |
| `just build` | `CGO_ENABLED=0 go build -trimpath -buildvcs=true -mod=readonly`; write `.artifacts/bin/tf-mut`. |
| `just fmt` | Apply canonical Go, Bash, JSON, YAML, TOML and explicitly rooted Terraform formatting. |
| `just fmt-check` | Diff-only equivalent of `fmt`; never changes the tree. |
| `just lint` | Run the exact pinned aggressive Go, Bash, data-file and GitHub Actions lint configuration; no fixes. |
| `just lint-go` | Run the aggressive golangci-lint configuration. |
| `just lint-shell` | Run `bash -n`, shfmt check and ShellCheck over the complete Bash manifest. |
| `just lint-config` | Parse and format-check JSON/YAML/TOML, then run Tombi lint with warnings as errors. |
| `just lint-actions` | Run actionlint and offline zizmor with the auditor persona and strict collection. |
| `just lint-fix` | Apply safe fixes and canonical formatting, repeat the sequence, and fail if the second pass changes the tree. |
| `just fix` | Apply convergent linter fixes and every canonical formatter. Explicitly mutating. |
| `just typecheck` | `go build` plus `go vet -tests=true` over the documented normal and integration tag matrix; do not execute tests. |
| `just mod-check` | `go mod tidy -diff`, `go mod verify` and read-only module loading. |
| `just test` | No-egress engine-seam suite using real Terraform, fixed shuffle/Rapid seeds, regression properties and fuzz corpus. |
| `just test-property` | Focused Rapid property/state-machine suite; also included by `test`. |
| `just test-random` | Scheduled randomized shuffle/Rapid run that records seeds and minimized regressions. |
| `just test-race` | The offline suite with `CGO_ENABLED=1 -race`; requires the checked C toolchain. |
| `just test-integration` | Explicit network/real-provider suite; requires opt-in credentials/safety flags. |
| `just test-performance` | Realistically sized provider-schema measurement; separate, explicit and artifact-producing. |
| `just fuzz PACKAGE TARGET DURATION` | Run exactly one named native fuzz target for a bounded time. |
| `just fuzz-all` | Discover targets from `go test -list='^Fuzz' ./...`, fail if empty, and invoke each with a bound. |
| `just mutate-diff BASE` | Gremlins `--integration --coverpkg './...' --diff BASE`; initially report-only and time-bounded. |
| `just mutate` | Full `--integration --coverpkg './...'` mutation sweep, normally scheduled. |
| `just security` | Required but time-varying vuln/dependency/secret/workflow scan; emit redacted diagnostics and zizmor SARIF. |
| `just agent-check` | Verify links, hook/rule syntax, trust documentation and one-way “interactive is never auto-allowed” safety invariants. |
| `just ci` | Hermetic, source-tree-clean PR bundle: doctor, format, modules, typecheck, lint, build and fixed-seed offline tests. |
| `just ci-full` | Scheduled bundle adding race, randomized properties, bounded fuzz and full mutation; security remains its own job. |

Every recipe should run tools through the mise environment or the repo-local `.tools/bin`, with
`GOTOOLCHAIN=local`; shell activation is not a Just prerequisite. The Justfile should set a
minimum Just version. Recipes that rewrite source or locks (`fmt`, `fix`, updates) must never be
dependencies of `ci`.

Keep all generated output under ignored `.artifacts/`: binaries, sandboxes, JUnit, Go test event
JSON, coverage profiles, mutation JSON and SARIF. A recipe must produce the same artifact paths
locally and in CI, and `just ci` must leave tracked and untracked source locations unchanged.

## Formatting and autofix

Run `goimports` first and `gofumpt -extra` second. goimports adds/removes imports and applies
gofmt-style formatting; gofumpt is a stricter formatting layer built on gofmt, and its extra
rules are the aggressive community default requested here. Use exact direct binaries rather than
golangci-lint's embedded formatter copies, which may lag the upstream releases. See
[gofumpt](https://github.com/mvdan/gofumpt) and
[goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports).

The same manifest-driven contract covers the other repository languages: shfmt owns Bash
layout; jq owns JSON; yamlfmt owns YAML; and Tombi owns TOML. Each manifest is compared with
repository-wide discovery in Go tests, so a new supported file cannot evade formatting or
linting. Bash is reserved for trivial orchestration; logic substantial enough to merit a test
belongs in Go. Bash itself therefore has parse and static-analysis gates but no test framework.

`fmt-check` should format a temporary copy or use diff-capable modes and fail with a readable
patch. It must prove the sequence is idempotent. Never run `terraform fmt -recursive` from the
repository root: it has no exclusion flag and would traverse provider caches, sandboxes and
vendored modules. Keep a committed list of valid fixture roots and invoke Terraform only on
those roots. A separate malformed-fixture skip list must contain exact paths; `fmt-check` fails
if a listed path no longer exists, preventing renamed fixtures from silently escaping checks.

Do not add `golines` initially. Automatic line wrapping is unusually churn-heavy, its ordering
with import/strict formatters needs another idempotence pass, and the upstream project no
longer offers a compelling enough benefit for this first chain. It can be reconsidered from
measured diffs.

Autofix stays opt-in:

- `just lint` is read-only;
- `just lint-fix` invokes safe linter fixes and the canonical formatter twice, failing if the
  second pass changes anything, then runs read-only lint;
- CI reports fixable differences but never rewrites a checkout.

## Linting and type checking

Start golangci-lint v2 with top-level `version: "2"` and `linters.default: all`. Pinning makes
this governable: a new upstream linter cannot appear mid-run. The checked-in configuration
should use:

- `govet` with all analyzers, including strict shadow checking;
- all Staticcheck checks;
- all applicable Revive rules;
- `nolintlint` requiring the exact rule name and an explanation;
- no broad default exclusion presets;
- unlimited issue reporting rather than first-N truncation;
- an explicit exception list whose entries each explain “conflict”, “deprecated” or why the
rule is inapplicable to this codebase.

GitHub Actions receive two independent blocking paths: actionlint performs syntax, expression
and embedded-ShellCheck analysis, while offline zizmor runs with `--persona=auditor` and
`--strict-collection`. Workflows must clear that maximal-recall profile without blanket
suppressions. Checkout credentials are not persisted, action references use immutable SHAs,
permissions are least-privilege, and workflow concurrency is explicit.

`default: all` will produce contradictory findings and, more dangerously, contradictory fixes.
The canonical formatter owns whitespace. Seed only the structurally known formatter-conflicting
exceptions, each with a reason, and prove `just lint-fix` converges. Do not add directory-wide
`nolint` comments or grandfather an unchecked baseline. The exception list is provisional until
M1 provides representative code; re-derive and review it then rather than calling an empty-module
configuration final. The relevant upstream references are the
[v2 configuration](https://golangci-lint.run/docs/configuration/file/),
[linter settings](https://golangci-lint.run/docs/linters/configuration/) and
[CI/version guidance](https://golangci-lint.run/docs/welcome/install/ci/).

Go has no separate TypeScript-like type checker. Aggressive type safety comes from several
independent compiler/analyzer paths:

1. `go build` over the documented normal and integration tag matrix for production packages;
2. `go vet -tests=true` over the same matrix to type-check test packages without executing
   `TestMain`, package initialization or Terraform setup;
3. golangci-lint's non-disableable typecheck findings, `govet` and Staticcheck;
4. `go test -race` for executed concurrency behaviour.

Do not describe `go test -run='^$'` as compile-only: it links and starts each test binary even
when no test function matches.

Do not add a separate Staticcheck binary gate initially; golangci-lint already embeds and
coordinates it. Do not make NilAway blocking initially either: it remains under active
development, deliberately trades false positives for recall, and requires a custom
golangci-lint build for integration. Pilot it later against real code and promote it only if
the signal justifies the maintenance cost.

## Tests, properties and fuzzing

Use gotestsum as the common wrapper around `go test -json`. It provides readable local output
and stable JUnit/event artifacts without changing test semantics. The hermetic gate uses
`-count=1`, a committed numeric `-shuffle` seed and explicit `RAPID_SEED`/`RAPID_CHECKS` values.
Rapid settings use environment variables because `go test ./...` passes custom command-line
flags to every package test binary, including packages that do not import Rapid.
The scheduled `test-random` recipe uses new seeds and always prints/records them. Do not use
automatic reruns to turn a flaky first attempt into a green gate. See
[gotestsum](https://github.com/gotestyourself/gotestsum) and the
[Go test flags](https://pkg.go.dev/cmd/go#hdr-Testing_flags).

The initial split is:

- **Offline/default:** real Terraform, isolated sandbox copies and only exact providers from the
  filesystem mirror, with no credentials or egress. Required in `just ci`.
- **Race:** the same suite with `CGO_ENABLED=1 -race`, required as a separate PR job once the
  Linux C-toolchain prerequisite is checked.
- **Integration:** real providers or network, build-tagged and guarded by explicit opt-in.
- **Performance:** the existing realistically sized provider-schema measurement, never inferred
  from the tiny null-provider fixture.

Coverage should be emitted for diagnosis and change review, but **not made a global percentage
gate**. This project's reason for existing is that line coverage is not a sufficient quality
signal. Establish mutation efficacy and mutation-coverage baselines, then ratchet those instead.

Add Rapid for properties and shrinking. It is deterministic only when the seed and check count
are explicit; a zero seed asks Rapid for a random one. High-value initial properties are:

- worker count and scheduling never change the result multiset;
- cancellation/error paths never modify the source tree;
- every generated report has internally consistent counts and classifications;
- deterministic fixture inputs yield deterministic result ordering/fingerprints;
- generated valid configurations exercised through the engine seam never bypass sandboxing.

When Rapid finds a failure, preserve the minimized case as a committed regression example or
failfile corpus and replay it in `just test`. Do not rely only on a seed that may exercise a
different path after generator code changes.

Rapid can bridge properties to native fuzzing. Native fuzz targets must be fast, deterministic
and free of external state. Ordinary `go test` always replays committed seed/regression corpora;
scheduled fuzzing discovers targets from `go test -list='^Fuzz' ./...`, fails when none exist,
and invokes one target at a time with a fixed time budget because Go supports only one selected
target per invocation. Commit minimized failures under `testdata/fuzz`. See the
[official fuzzing guide](https://go.dev/doc/security/fuzz/).

## Mutation testing

Use Gremlins, pinned at 0.6.0 only after a spike proves it understands a Go 1.26 module. Because
the authoritative tests cross package boundaries at the top engine seam, recipes need two
independent options: **integration mode** makes every relevant test package run, while
`--coverpkg './...'` attributes coverage to the internal packages those tests exercise. Either
omission can silently misclassify mutants.

The pull-request recipe should be conceptually equivalent to:

```text
gremlins unleash --integration --coverpkg "./..." --diff <reviewed-base> --output <artifact>
```

CI needs sufficient Git history to resolve the base. Plant an acceptance mutant in a non-seam
package and require it to be `KILLED`, not `NOT COVERED`; record the not-covered count explicitly.
Start the PR recipe report-only with a ten-minute budget. A budget expiry is non-blocking only
when a valid report provides processed and remaining mutant counts; an evidence-free timeout
fails. Record a
baseline, inspect equivalent/uncovered cases, and only then introduce reviewed thresholds. Run
full-module mutation on a schedule; Gremlins is pre-1.0 and full sweeps can take hours. Its
documented threshold, JSON, diff, integration and cover-package options are described in
[Gremlins unleash](https://gremlins.dev/latest/usage/commands/unleash/).

This mutation gate tests the quality of tf-mut's Go tests. It is distinct from tf-mut running
Terraform mutations as its product behaviour; recipe names and artifacts must keep that
distinction clear.

The implementation spike found that Gremlins recursively copies its input directory and panics
when that directory includes live repo-local Go/mise caches. The recipes therefore construct a
clean temporary source staging tree from Git's tracked and non-ignored file set, point diff mode
at the original Git metadata, and serialize the initial worker pool. With that adapter, Go 1.26.6
completed six mutations at 100% efficacy and mutant coverage, including a `KILLED` mutant in
`internal/buildinfo` observed by tests in `cmd/tf-mut`. The full recipe asserts that cross-package
acceptance case from Gremlins' JSON output.

## Security chain

`just security` should be a required, explicitly time-varying job separate from hermetic
`just ci`, and contain complementary checks:

1. `go mod verify` and `go mod tidy -diff` for dependency integrity and manifest drift;
2. `govulncheck -test ./...` for reachable Go vulnerabilities, including test code;
3. golangci-lint's pinned gosec analyzer over production and test code, using only
   `//nolint:gosec // reason` suppressions enforced by `nolintlint`;
4. OSV-Scanner v2 recursively over the source tree for ecosystem/lockfile advisories that
   reachability analysis does not cover;
5. Gitleaks with `--redact` over the current change in PRs and full history on a
   schedule/release gate;
6. actionlint plus zizmor auditor-mode analysis of Actions and Dependabot, with SARIF retained;
7. verification that provider constraints, lock hashes, allowlist and filesystem mirror agree.

Do not install or run a second standalone gosec version. If SARIF is required, emit it from the
same golangci-lint/gosec configuration rather than creating a second finding and suppression
universe.

Govulncheck's ordinary textual invocation must be the blocking one. Its JSON, SARIF and OpenVEX
output modes intentionally exit zero even when they find vulnerabilities, so generate those
artifacts in a second invocation rather than treating SARIF generation as the gate. See the
[govulncheck documentation](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck#hdr-Exit_codes),
[OSV-Scanner](https://google.github.io/osv-scanner/) and
[Gitleaks](https://github.com/gitleaks/gitleaks).

The first live scan found that gopls v0.23.0's graph contains three advisory-affected modules.
The fixed `x/mod` release requires `x/tools` v0.49.0, which does not compile gopls v0.23.0.
Because there is no fixed stable gopls release, OSV excludes only `tools/gopls`; the blocking
gate instead runs current govulncheck symbol analysis on the exact Go 1.26.6-built gopls binary.
That analysis found the affected modules but no reachable vulnerable symbols. Root, goimports,
and govulncheck manifests remain under OSV's non-reachability-filtered scan; the latter two pin
the fixed `x/mod` v0.40.0. Remove this narrow build-tool exception as soon as a compatible stable
gopls release exists.

Pin scanner binaries exactly, but expect their advisory databases to change. Record the scan
time/database metadata in artifacts. Local/CI parity means the same recipe and arguments, not
pretending an online vulnerability result is frozen forever. Cache only where the scanner
documents safe invalidation.

The first security-specific tests should target this product's actual attack surface: sandbox
path traversal, symlink escape, hostile module layouts, subprocess cancellation, environment
leakage, unsafe Terraform opt-in gates and redaction of secrets from reports. Dependency scans
cannot replace those behavioural tests.

CodeQL advanced setup analyzes every repository language it supports: `go` with a manual
`just build`, and `actions` with build mode `none`. Both use GitHub's
`security-and-quality` query suite, the maximal built-in suite and a superset of
`security-extended`. The workflow is SHA-pinned and least-privilege. Extraction and result
upload necessarily run in GitHub's code-scanning environment, while the Go build and all
ordinary quality gates remain the same Just recipes used locally. The repository is public so
GitHub code scanning is available without a private-repository entitlement.

SBOM/license inventory and signed release provenance remain sensible release-phase additions.
They are not initial blocking gates until the project chooses a license and has release
artifacts.

## CI topology

The workflow file should contain orchestration only:

1. check out the repository, with full history only for mutation/history jobs;
2. bootstrap mise at a commit-SHA-pinned version;
3. run the one direct locked mise install, then `just tools-install` for the Go tools and
   provider mirror, with caches keyed by every applicable lock, OS and architecture;
4. invoke the same Just recipes available locally;
5. upload the artifacts those recipes produced, with SARIF upload restricted to trusted events.

Recommended jobs:

| Trigger | Required recipes |
| --- | --- |
| Pull request | `just ci`, time-varying `just security`, `just test-race`, report-only/time-bounded `just mutate-diff <base>` |
| Main branch | the pull-request recipes; mutation can block only after baseline and budget review |
| Nightly | `just ci-full`, `just security` with full-history Gitleaks |
| Explicit/network | `just test-integration`, `just test-performance` on trusted push/manual events only |
| Weekly update | `just tools-update`, `just providers-update`, `just deps-update`, then open a pull request |
| Release | Linux-x64 `just ci-full`, security/history scan and two-build digest comparison; later SBOM/signing |

The separate CodeQL workflow runs Go and Actions analysis on pull requests, the default branch,
and a weekly schedule, always with `security-and-quality`.

The workflow security contract is part of the design, not an implementation detail:

- use `pull_request`, never `pull_request_target`, for code from pull requests;
- default to `permissions: contents: read`; elevate only the individual trusted job that needs
  a write scope;
- run untrusted code only on ephemeral GitHub-hosted runners with no credentials, protected
  environments or writable persistent cache; restore-only caches are preferable for forks;
- make provider sources/versions an allowlist so an unexpected fixture fails rather than being
  downloaded accidentally; this is a reproducibility guard, not a substitute for runner isolation;
- make credentialed integration/performance jobs unreachable from fork pull requests;
- generate SARIF on pull requests if useful, but upload it only from trusted push/schedule events.

Parallel CI jobs are an execution optimization, not different behaviour. A contributor can run
every job command locally. Conversely, raw `go test`, bespoke linter actions and GitHub-only
mutation wrappers do not belong in the workflow. Pin third-party actions by immutable SHA and
let a workflow-scoped Dependabot configuration propose SHA updates. Opening an update pull
request is CI orchestration, so it need not make `gh` a build-chain dependency.

## Claude Code and Codex parity

Aim for semantic parity through shared repository truth, while acknowledging that the two
harnesses do not expose identical extension APIs.

### Instructions

Move the current `CLAUDE.md` content to canonical `AGENTS.md`, then commit
`CLAUDE.md -> AGENTS.md` as a symbolic link. Claude's documentation explicitly supports this
layout, and Codex natively reads `AGENTS.md`. On Windows without symlink support, Claude's
documented `@AGENTS.md` import is the fallback, but the canonical repository representation
should remain the symlink requested here. See
[Claude project memory](https://code.claude.com/docs/en/memory) and
[Codex AGENTS.md](https://learn.chatgpt.com/docs/agent-configuration/agents-md).

Keep durable facts in that file: reading order, fixed test seam, safety rules and the Just
command map. Do not duplicate them in harness-specific rules. If path-specific guidance becomes
necessary, prefer nested canonical `AGENTS.md` plus the smallest Claude adapter rather than two
independently maintained essays.

### Skills

Create one small project-development skill that teaches both agents when to run `just fix`,
`just ci`, the focused test/fuzz/mutation recipes, and the real-Terraform safety boundaries.
Keep the canonical skill in `.claude/skills/tf-mut-development/SKILL.md` and symlink
`.agents/skills/tf-mut-development` to it; Codex documents project skills under `.agents/skills`
and follows symlinked skill directories, while Claude documents `.claude/skills` and the shared
Agent Skills format. Do not conflate this developer skill with tf-mut's planned product-facing
mutation and characterisation skills.

References: [Claude skills](https://code.claude.com/docs/en/skills) and
[Codex skills](https://learn.chatgpt.com/docs/build-skills).

### LSP and diagnostics

Pin gopls in `tools/gopls/go.mod`, build it to `.tools/bin/gopls`, and keep its editor features from
silently duplicating differently versioned quality tools:

```json
{
  "gofumpt": false,
  "staticcheck": false,
  "vulncheck": "Off"
}
```

Claude Code has a documented LSP-plugin mechanism and its documentation includes the exact
`gopls serve` adapter shape. Commit a project-scoped skills-directory plugin with its own
`.claude-plugin/plugin.json` and LSP definition. Its checked-in launcher finds the repository
root and executes `.tools/bin/gopls serve`, so it does not depend on shell activation. `doctor`
reports a clear editor prerequisite until `tools-install` has built that binary. This provides
basic diagnostics, navigation and type information; canonical formatting, Staticcheck and
vulnerability findings remain the exact Just recipes. See the
[Claude LSP plugin reference](https://code.claude.com/docs/en/plugins-reference#lsp-servers)
and [gopls settings](https://go.dev/gopls/settings).

No equivalent supported repo-native Codex LSP attachment was found in the current Codex manual.
Do not claim false parity or build automation around the explicitly unstable gopls CLI.
Codex still receives the same authoritative compiler, lint and vulnerability diagnostics by
running focused Just recipes; editor integrations can use the same pinned gopls. Native
real-time LSP is an explicit harness capability gap in this proposal.

### Hooks, rules and permissions

Commit thin native hook configurations for both harnesses, but make every command call the
same shared Just recipe or checked-in handler. Project hooks/rules take effect only after the
workspace is trusted; document that prerequisite. Good initial hooks are deliberately cheap:

- session start: `just doctor` in concise mode;
- after editing Go/HCL: focused format-check or diagnostics, never an unconditional tree rewrite;
- stop: `just agent-check` plus a focused check chosen from changed files.
- before edits to `Justfile`, lock/config files, `.claude/**` or `.codex/**`: request explicit
  review in each harness where its hook API supports that decision.

Do not put full mutation, fuzz, network integration or tool updates in an automatic hook.
Claude and Codex both support lifecycle hooks, but their JSON schemas and exact event payloads
are adapters, not a reason to duplicate the underlying policy. See
[Claude hooks](https://code.claude.com/docs/en/hooks) and
[Codex hooks](https://learn.chatgpt.com/docs/hooks).

Claude can return `permissionDecision: "ask"` for a protected edit. Current Codex
`PreToolUse` hooks explicitly do not support `permissionDecision: "ask"`; the Codex adapter
instead injects visible review context before Bash/Edit/Write calls, while execpolicy retains
interactive prompts for commands it can classify. This is the closest supported semantic
mapping, not a claim that the APIs are identical.

Claude permission rules and Codex `.codex/rules/*.rules` are different predicates; Codex rules
are experimental execpolicy for out-of-sandbox commands, not behavioural Markdown. A Just
recipe name is not a security boundary because an agent can edit the Justfile. Do not auto-allow
recipes merely because their current implementation appears read-only—builds write artifacts,
tests execute repository code, and security scans use egress.

Define the cross-harness invariant in one direction only: every intent marked interactive
(updates, releases, networked/credentialed execution, infrastructure opt-ins and policy-file
changes) must not be auto-allowed in either harness. `agent-check` validates that negative
property and adapter syntax; it does not claim the two permission systems are equivalent or
active on an untrusted workspace. Treat repo rules and protected-file hooks as best-effort
defence in depth, not as the sandbox or trust boundary.

## Proposed repository layout

```text
AGENTS.md
CLAUDE.md -> AGENTS.md
Justfile
mise.toml
mise.lock
tools/gopls/go.mod
tools/gopls/go.sum
tools/goimports/go.mod
tools/goimports/go.sum
tools/govulncheck/go.mod
tools/govulncheck/go.sum
.golangci.yml
.github/workflows/ci.yml
.github/dependabot.yml
test/fixtures/*/.terraform.lock.hcl
tools/providers.allowlist
tools/terraform-cli.tfrc
.claude/
  settings.json
  skills/tf-mut-development/SKILL.md
  skills/tf-mut-gopls-plugin/
    .claude-plugin/plugin.json
    .lsp.json
    launch-gopls
.agents/
  skills/tf-mut-development -> ../../.claude/skills/tf-mut-development
.codex/
  hooks.json
  rules/just.rules
scripts/
  agent-hook
  check-agent-parity
.tools/                       # ignored; source tools and provider mirror
.artifacts/                  # ignored
```

The scripts are implementation details behind Just. They should be small and never become a
competing public command surface. Bash is limited to trivial adapters and always passes shfmt,
`bash -n`, and ShellCheck. Move logic worthy of testing into Go rather than growing a shell test
surface.

## Rollout

Implement the chain in four reviewable stages:

1. **Bootstrap and lock spikes:** declare Linux x86-64 support; prove every mise backend passes
   strict locked install; create the root and tools Go modules; set `GOTOOLCHAIN=local`; commit
   exact provider constraints/locks/allowlist; populate the filesystem mirror; add Just, `doctor`,
   build and module checks. Stage 2 cannot start until tool and provider installation works with
   version resolution disabled.
2. **Quality:** formatter/checker, aggressive golangci configuration, gotestsum, offline
   engine-seam fixtures, race prerequisites, fixed/random Rapid recipes and seed-corpus fuzz
   tests. Document structural lint conflicts now; re-derive the exception list against M1 code.
3. **Security and mutation:** time-varying blocking security job and artifacts; prove Gremlins
   supports Go 1.26 and kills a non-seam-package mutant with `--integration --coverpkg`; collect
   timing/coverage baselines before choosing mutation thresholds or a blocking PR budget.
4. **Harness and CI:** canonicalize `AGENTS.md`, add the symlink, shared skill and hook handlers,
   one-way permission-safety test, gopls plugin, then add least-privilege SHA-pinned workflows,
   Dependabot and scheduled update PRs.

Acceptance evidence for the initial chain should include:

- a fresh strict locked install on Linux x86-64, with every prebuilt tool carrying a URL and
  checksum and every source tool verified by `go.sum`;
- `just test` succeeds with egress blocked, using the filesystem mirror and exact provider locks;
- `just ci` leaves every tracked and non-ignored source path, mode, symlink target and byte
  content unchanged, including files already dirty when the gate starts;
- `just fmt` is idempotent and `just lint-fix` reaches a fixed point on its second pass;
- two clean `just build` invocations of the same commit produce identical Linux-x64 digests;
- every CI command maps to a documented local Just recipe;
- intentionally bad formatting, type, lint, race, property, mutation, vulnerability and secret
  fixtures each fail the intended gate;
- the Gremlins acceptance case in a non-seam package is `KILLED`, never `NOT COVERED`;
- offline tests prove Terraform is real, provider sources are allowlisted and the source tree is
  unchanged;
- workflow tests reject `pull_request_target`, excessive token permissions, fork access to
  credentialed jobs and unredacted secret scans;
- the Claude/Codex check detects broken links, malformed adapters or an interactive intent that
  either harness auto-allows.

## Risks and explicit non-decisions

- “All linters” is intentionally noisy. The exact pin and reviewed exception list make that
  noise governable; they do not make every linter universally correct. The initial exception
  list is provisional until representative M1 code exists.
- Gremlins is pre-1.0, predates Go 1.26 and may be slow. Compatibility, cross-package coverage
  and timing spikes precede any blocking threshold.
- Vulnerability databases are live inputs. Preserve tool/command parity and record scan time;
  keep them outside the hermetic gate and do not promise identical findings across dates.
- The initial supported platform is Linux x86-64 only. Expanding the matrix requires tool locks,
  provider hashes, race prerequisites and symlink behaviour to be proven on the new platform.
- Strict mise installation is conditional on the chosen full-support backends passing the Stage 1
  spike; no version-only exemption is silently accepted.
- Race and fuzz checks only explore executed paths. They complement, rather than replace,
  properties, mutation and static analysis.
- Global line-coverage thresholds, golines, NilAway, SBOM/signing and automatic release
  publication are deferred for stated reasons. Deferral is not rejection; each should enter
  only with a measured signal and a Just recipe that works locally.
- Claude/Codex native LSP, hooks and permission systems are capability gaps, not equivalent
  adapters. The shared instructions, skills and authoritative Just commands provide semantic
  parity where the products permit it; the one-way safety invariant prevents over-claiming.

## Primary references

- [Go dependency/tool management](https://go.dev/doc/modules/managing-dependencies)
- [Go toolchains](https://go.dev/doc/toolchain)
- [Go fuzzing](https://go.dev/doc/security/fuzz/) and
  [race detector](https://go.dev/doc/articles/race_detector)
- [mise lockfiles](https://mise.jdx.dev/dev-tools/mise-lock.html) and
  [CI guidance](https://mise.jdx.dev/continuous-integration.html)
- [Terraform dependency locks](https://developer.hashicorp.com/terraform/language/files/dependency-lock) and
  [provider installation mirrors](https://developer.hashicorp.com/terraform/cli/config/config-file#provider-installation)
- [Just manual](https://just.systems/man/en/)
- [golangci-lint v2 configuration](https://golangci-lint.run/docs/configuration/file/)
- [gopls documentation](https://go.dev/gopls/)
- [Gremlins documentation](https://gremlins.dev/latest/)
- [Go vulnerability management](https://go.dev/doc/security/vuln/)
- [GitHub Actions permissions](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#permissions)
- [Claude Code project instructions](https://code.claude.com/docs/en/memory),
  [skills](https://code.claude.com/docs/en/skills),
  [hooks](https://code.claude.com/docs/en/hooks) and
  [LSP plugins](https://code.claude.com/docs/en/plugins-reference#lsp-servers)
- [Codex AGENTS.md](https://learn.chatgpt.com/docs/agent-configuration/agents-md),
  [skills](https://learn.chatgpt.com/docs/build-skills) and
  [hooks](https://learn.chatgpt.com/docs/hooks)
