# tf-mut — agent instructions

Mutation testing and characterisation-test scaffolding for `terraform test`, optimised for
fully-mocked unit tests. Milestones M1–M4.5 are implemented: `tf-mut run` and `tf-mut
preview` drive Tiers 0–3 end to end against real Terraform, every survivor carries one
diagnosis from the fingerprint oracle, the attribute-level reference graph sharpens the
oracle behind fail-closed adapters, `--since`, the verdict cache and the baseline gate table
make runs fast and CI honest, and seven reporters (terminal, JSON, SARIF,
mutation-testing-elements, HTML, JUnit, markdown) derive from one report value. M4 adds the
JSON safety floor and the discover-only JSON slice (`.tf.json`/`.tftest.json` feed the
inventories and the graph, never the mutation surface), `tf-mut suggest` — verified
suggested assertions behind three fail-closed adapters, applied under a snapshot-bound
protocol — and `tf-mut skill install`. M4.5 adds the generation direction: `tf-mut
characterise` scaffolds, harvests, pins and verifies a first suite for a module that has
none, with the safety gates judged against the effective staged suite; `tf-mut todos`,
`--answer` and `--resume` drain the judgement points it refuses to guess at, through a
non-executable artefact class that `terraform test` never reads; `--until-dry` closes the
gap over a staged overlay that writes nothing; and `tf-mut curate` reports redundancy from
authoritative populations only. Later milestones are still specified in GitHub issues
labelled `ready-for-agent`.

## Reading order

1. `README.md` — problem, verified findings, document map
2. `docs/research/05-go-build-chain.md` — accepted local/CI build-chain contract
3. `docs/design/product-design.md` — architecture, mutant states, metrics, CLI, roadmap
4. `docs/reviews/` — **all adversarial reviews and their dispositions. Read before changing
   any design decision**: many decisions exist specifically because a review refuted the
   obvious alternative, with experiments
4a. `docs/research/12-m45-synthesis-rate.md` — the measurement that gated M4.5b, its
   decision rule and the two costs it surfaced. `docs/research/13-m45-exit-gate.md` — what
   implementing M4.5 measured, decided and deferred
4b. `docs/reviews/2026-08-16-m2-implementation-review.md` and
   `docs/research/08-m2-exit-gate.md` — what implementing M2 measured, decided and deferred,
   the contract sweep from every normative behaviour to its test, and the reproduction map.
   **Read both before writing the next milestone spec**: they carry the measurements that
   outrank the design prose, and the open questions the next spec has to dispose of. M1's
   equivalents (`2026-08-16-m1-implementation-review.md`, `docs/research/06-m1-exit-gate.md`)
   remain the record for the Tier 0 loop
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

**Recorded exceptions, each narrow, each here rather than taken quietly.** None may be
widened without amending this section.

1. `internal/fingerprint` is tested directly on payload *shapes* — null against absent against
   empty, an addressed collection Terraform reordered, a value that changed type between two
   runs. These are contracts about documents, and driving the real binary into producing each
   shape on demand is not possible; the shapes themselves are taken from recorded real output.
   Every *verdict* the shapes lead to is still asserted through the engine seam.
2. `internal/tfexec` is tested directly for the streaming memory gate, which is a claim about
   this project's retained heap and has no exported surface to make the claim about. Its input
   is recorded real `terraform test -verbose -json` output with the provider schemas inflated;
   the M2 spec is explicit that manipulating recorded real output "is not a fake runner".

3. The M3 reference graph's adapters are exercised directly over `discovery.BuildGraph`,
   `SiteCone` and cone membership (`internal/engine/graph_test.go`), because issue #44
   mandates it: the fail-closed adapter sweep runs "over every operator generation site in
   the applicability-matrix fixture", and the supplemental `terraform graph` comparison is
   test-suite-only by the M3 spec review's M1 disposition. Every verdict the graph leads to
   — path-scoped unknowns, static `Unobservable`, conditional `NoCoverage` — is still
   asserted through the engine seam.
4. The M4 suggestion engine's three adapter matrices (`internal/suggest/adapters_test.go`)
   are exercised directly over canonical payload paths and published provider types, for
   exception 1's own reason: they are contracts about documents and types, and the real
   binary cannot be driven into each of the fifteen shapes on demand. Every adapter outcome
   is still asserted through the engine seam (`internal/engine/suggest_test.go`).
5. The apply protocol's preflight refusals that no engine-seam test can stage — a digest
   that went stale between verification and write (a concurrent-editor race), a target
   outside the closure, a file whose bytes stopped parsing — are exercised directly over
   `preflight` (`internal/engine/apply_internal_test.go`). Within one invocation
   verification always precedes apply, so the engine seam cannot produce these states
   deterministically; every refusal the seam *can* stage (symlink, non-verified selection,
   JSON target, partial failure) is still asserted through it (`apply_test.go`).

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
| `internal/mutation` | The operator catalogue and its applicability matrix. Tier 0 is applied through `hclwrite`; Tiers 1–3 rewrite byte ranges, so a mutant differs from the original only in the tokens its operator owns. Content-derived identifiers; deduplication; diffs |
| `internal/fingerprint` | The oracle's arithmetic: canonical payload projection, the volatile mask, the masked delta. Decides what two runs can honestly be said to have in common, and never a verdict |
| `internal/suggest` | The suggestion engine: the address, rendering and sensitivity adapters, the run-block patch writer, and the stable suggestion identity |
| `internal/characterise` | The generation direction: the scaffold planner, the mock and scenario renderer, the input-synthesis preference pipeline with its static validation evaluator, the granularity ladder and the pinning stage. Pins go through `internal/suggest`'s adapters unchanged, so a value that is unrenderable for one is unrenderable for both |
| `internal/skill` | The shipped agent skills and the `skill install` write protocol |
| `internal/config` | `.tf-mut.hcl` and the inline suppression directives |
| `internal/sandbox` | Closure-rooted materialisation, provider and remote-module sharing, fresh-inode writes |
| `internal/tfexec` | The Terraform CLI: `version`, `init`, `validate`, `providers schema`, `fmt`, and the `test -json` stream |
| `internal/report` | The report value, its state, diagnosis and metric definitions, and the terminal, JSON and SARIF renderings |

The JSON reporter's contract is published at `docs/schema/report-2.3.0.json` and validated in
the suite; `report-2.2.0.json`, `report-2.1.0.json`, `report-2.0.0.json` and
`report-1.0.0.json` remain published for earlier consumers. The characterisation block's
status vocabularies are closed: extending one is a minor schema version with the consumer
contract documented, not a silently additive change. Changing a field's name or meaning means a new schema version and a new file.
SARIF output is validated against the vendored `docs/schema/sarif-2.1.0.json`; the
mutation-testing-elements adapter against the vendored
`docs/schema/mutation-testing-report-2.0.0.json` (declared lossy — tf-mut's metrics are the
authoritative ones); JUnit structurally against `docs/schema/junit-jenkins.xsd`.

The operator catalogue's **applicability matrix** (`docs/design/mutation-operators.md`) is
normative and enforced: a row naming an operator the catalogue does not enable, an enabled
operator with no row, and an operator with no generation site in the fixtures are each a test
failure. Adding an operator means adding its row and its site in the same change.

Four rules the engine enforces that are easy to break by accident. The safety gates are decided
statically **before** any Terraform runs — a provisioner must not execute in order to be
refused, and no configured exclusion may reach them. `terraform validate` runs **only** after a
run-level error, where it is the sole discriminator between `Invalid` and `KilledByError`.
Phase two runs **only** for phase-one survivors, because `-verbose` costs 20,288× the output
volume. And the oracle never claims an equality it cannot prove: an unknown value in the
mutation's forward cone — judged under the fail-closed address adapters, with the M2
whole-payload rule as the floor wherever a mapping fails — or volatility it could not
decompose, makes the comparison indeterminate rather than identical.

`just gate` runs the M2a honesty gate — the reproductions the oracle has to survive —
`just gate-m3` runs the M3 offline gates: graph soundness, the count levers and the gate
table, and `just gate-m4` runs the M4 offline gates: the JSON safety floor, the
suggestion-soundness gate, the apply protocol and the skill contract, and `just gate-m45`
runs the M4.5 offline gates: the #70 collectors in both syntaxes, the scaffold-soundness
gate, the TODO protocol, the until-dry loop, curate's population posture and the
end-of-MVP walkthrough — each audited by name exactly as the honesty gate is.
`just measure-synthesis` is the M4.5-0 corpus measurement, network-gated and separate. All are separate recipes from `just test` on purpose:
operator and interface breadth must not be able to hide a failed oracle behind a large green
checklist. Per gate, two tests keep the recipe honest by checking that every name in it
resolves to a test that exists, and that every reproduction the spec requires is still
named.

## Conventions

- Implementation language is Go with `github.com/hashicorp/hcl/v2` — settled, see
  `docs/research/03-hcl2-tooling.md`.
- Keep Bash helpers to trivial orchestration. Put logic that merits tests in Go; every Bash
  helper must remain in `tools/shell-files` and pass the shared parse, format and lint gates.
- Keep CI thin: bootstrap locked tools, then invoke the same Just recipes used locally.
- Preserve `GOTOOLCHAIN=local`; toolchain drift must fail rather than download another Go.
- The source tree of a module under test is never written to. Sandboxes only. Four recorded
  exceptions, each a tool-owned write the user asks for by name: the verdict cache in the
  project-local `.tf-mut-cache/` directory (M3 spec review M6; `--no-cache` removes even
  that); the acceptance list `.tf-mut-baseline.json`, written only on an explicit
  `--write-baseline` over a full, unsampled, freshly executed population; the test files
  `suggest --apply` writes, bound to the verified source digest under the snapshot-bound
  preflight-then-atomic-rename protocol (M4 spec review C6, `internal/engine/apply.go`) —
  and never a JSON test file; and the skill files `skill install` places, atomic,
  user-edits-preserved unless `--force` (M4 spec review M4, `internal/skill`). Module
  sources themselves are never written.
- New milestone specs: run `/to-spec` against the design docs; one milestone per spec;
  absorb the previous milestone's implementation learnings first — they live in
  `docs/reviews/<date>-<milestone>-implementation-review.md` and the milestone's exit-gate
  document under `docs/research/`. Where a spec restates a review finding, **quote it rather
  than paraphrase**: M1's one costly conflict was an acceptance criterion that reworded a
  disposition and inverted it.
- Safety gates (`--allow-real-infrastructure`, `--allow-unsandboxed-effects`) are
  load-bearing product decisions, not defaults to soften.
- Engine fixtures live in `internal/engine/testdata/`. They are `terraform_data`-based and
  offline unless the name says otherwise; `mocked-null`, `mocked-aliases` and `unmocked` need
  the provider mirror (`just tools-install`) and skip without it, and `aws-mocked` is
  network-gated behind the `integration` tag. Every fixture is in the Terraform format manifest
  except `unformatted`, which is named in `tools/terraform-format-skip` with its reason: an
  unformatted fixture makes `hclwrite` re-align the file it round-trips, which silently turns
  every Tier 0 mutant's diff into a whole-file one.
- Intentionally malformed JSON fixtures are named in `tools/json-files-skip` with their
  reason, mirroring the Terraform format skip file; everything else with a `.json`
  extension must be in `tools/json-files`.
- `.golangci.yml` disables a handful of linters with a stated reason each. Adding a disable is
  allowed; adding one without the reason is not.
