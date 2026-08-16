# tf-mut — agent instructions

Mutation testing and characterisation-test scaffolding for `terraform test`, optimised for
fully-mocked unit tests. Milestone M1 is implemented: `tf-mut run` and `tf-mut preview` drive
the Tier 0 loop end to end against real Terraform. Later milestones are still specified in
GitHub issues labelled `ready-for-agent`.

## Reading order

1. `README.md` — problem, verified findings, document map
2. `docs/research/05-go-build-chain.md` — accepted local/CI build-chain contract
3. `docs/design/product-design.md` — architecture, mutant states, metrics, CLI, roadmap
4. `docs/reviews/` — **all adversarial reviews and their dispositions. Read before changing
   any design decision**: many decisions exist specifically because a review refuted the
   obvious alternative, with experiments
4a. `docs/reviews/2026-08-16-m1-implementation-review.md` and
   `docs/research/06-m1-exit-gate.md` — what implementing M1 measured, decided and deferred,
   and the reproduction map from review case to fixture to test. **Read both before writing
   the next milestone spec**: they carry the measurements that outrank the design prose, and
   the open questions the next spec has to dispose of
5. `docs/design/mutation-operators.md`, `characterisation.md`, `agent-integration.md`
6. `docs/research/01–04` — the verified factual base ([verified] = established by running
   Terraform v1.15.8, not by reading documentation)

## Precedence on conflict

Review dispositions (`docs/reviews/`) > the milestone spec (GitHub issue) > design prose.
If you find a genuine conflict, fix the losing document in the same change.

## Standing process rules (earned, not aspirational)

1. **No performance or feasibility claim enters a design document until measured against a
   realistically-sized provider schema.** Round-one review: all four criticals traced to
   measuring against a 3.4 KB provider schema when `hashicorp/aws` is 14.5 MB.
2. **A repair to a reviewed finding is unreviewed design until it has itself survived review
   or reproduction.** Round-two review: three of five criticals were introduced by round-one
   repairs.
3. **Never trust a plausible reading of Terraform behaviour — run it.** This project's
   research corrected four widely-repeated false claims (`-filter` scope, mock+apply,
   `relevant_attributes`, mock determinism). Fixtures in `research/spikes/` are cheap to
   extend; extend them.
4. **A speed lever is unbuilt design until its cost model has been measured on the fixture it
   targets.** M1 measured parallelism at 1.08× against a real provider schema where the spike
   had measured 3–8× against a 3.4 KB one, and the bottleneck turned out to be a cost no
   harness change reaches. Specify the experiment before the lever, not after.
5. **The tool controls the environment it hands Terraform.** Inheriting the caller's
   ambient environment wholesale broke remote module installation under an exported
   `GIT_DIR`, and let a fixture write into the real checkout's git configuration. Anything
   that redirects a subprocess at another location is stripped; anything that authenticates
   one is kept.

## Testing seam (fixed decision — do not reopen per milestone)

One seam, at the top: the engine entry point — configuration in, report out — exercised
end-to-end against the **real Terraform binary** on fixture modules. No fake Terraform
runner: both reviews proved the correctness risk lives in real Terraform behaviour (inode
sharing, implicit run-block state, evaluation-time errors, identical static/dynamic
diagnostics). Tests assert external behaviour only (report states, findings, metrics, exit
codes), never internals. Fixtures are null-provider/`terraform_data`-based, offline and
millisecond-fast; the one real-provider performance measurement is network-gated and kept
separate. The R1/R2 reproduction cases (recipes in review docs and issue #1) are mandatory
fixtures.

## Build and verification

Linux x86-64 is the initial supported development platform. Install the mise version pinned in
`mise.toml`, trust this workspace, then bootstrap the exact locked tools:

```bash
mise trust
MISE_CONFIG_DIR="$PWD/.artifacts/mise-config" mise install --yes
MISE_CONFIG_DIR="$PWD/.artifacts/mise-config" mise exec -- just tools-install
```

After bootstrap, Just is the only public control plane. Run it through mise unless project shell
activation already selects the locked binaries: `mise exec -- just ci`. Use `just fmt` or
`just lint-fix` only when source rewrites are intended. `just security` is a separate,
time-varying gate. Networked or real-infrastructure tests require both their explicit recipe and
`TF_MUT_ALLOW_REAL_INFRASTRUCTURE=1`.

When changing Go, Terraform fixtures, build-chain files, CI, or agent adapters, use the shared
`tf-mut-development` skill. It gives the shortest focused red/green route without duplicating
this repository contract.

## Implementation map

| Package | Responsibility |
| --- | --- |
| `internal/engine` | The seam. `Run(ctx, Config) (Report, error)` — version gate, safety gates, baseline, generation, execution, classification, findings |
| `internal/discovery` | `hclsyntax` parsing of modules and `.tftest.hcl` files; the `..`-closure; provider and effect inventories; reference forms |
| `internal/mutation` | The Tier 0 catalogue, applied through `hclwrite`; content-derived identifiers; deduplication; diffs |
| `internal/sandbox` | Closure-rooted materialisation, provider and remote-module sharing, fresh-inode writes |
| `internal/tfexec` | The Terraform CLI: `version`, `init`, `validate`, `providers schema`, `fmt`, and the `test -json` stream |
| `internal/report` | The report value, its state and metric definitions, and the terminal and JSON renderings |

The JSON reporter's contract is published at `docs/schema/report-1.0.0.json` and validated in
the suite. Changing a field's name or meaning means a new schema version and a new file.

Two rules the engine enforces that are easy to break by accident: the safety gates are decided
statically **before** any Terraform runs (a provisioner must not execute in order to be
refused), and `terraform validate` runs **only** after a run-level error, where it is the sole
discriminator between `Invalid` and `KilledByError`.

## Conventions

- Implementation language is Go with `github.com/hashicorp/hcl/v2` — settled, see
  `docs/research/03-hcl2-tooling.md`.
- Keep Bash helpers to trivial orchestration. Put logic that merits tests in Go; every Bash
  helper must remain in `tools/shell-files` and pass the shared parse, format and lint gates.
- Keep CI thin: bootstrap locked tools, then invoke the same Just recipes used locally.
- Preserve `GOTOOLCHAIN=local`; toolchain drift must fail rather than download another Go.
- The source tree of a module under test is never written to. Sandboxes only.
- New milestone specs: run `/to-spec` against the design docs; one milestone per spec;
  absorb the previous milestone's implementation learnings first — they live in
  `docs/reviews/<date>-<milestone>-implementation-review.md` and the milestone's exit-gate
  document under `docs/research/`. Where a spec restates a review finding, **quote it rather
  than paraphrase**: M1's one costly conflict was an acceptance criterion that reworded a
  disposition and inverted it.
- Safety gates (`--allow-real-infrastructure`, `--allow-unsandboxed-effects`) are
  load-bearing product decisions, not defaults to soften.
- Engine fixtures live in `internal/engine/testdata/`. They are `terraform_data`-based and
  offline unless the name says otherwise; `mocked-null` and `unmocked` need the provider
  mirror (`just tools-install`) and skip without it, and `aws-mocked` is network-gated behind
  the `integration` tag.
- `.golangci.yml` disables a handful of linters with a stated reason each. Adding a disable is
  allowed; adding one without the reason is not.
