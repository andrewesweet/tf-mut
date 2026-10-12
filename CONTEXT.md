# tf-mut context

This repository is one Go module and one documented context. The bounded contexts
below are package groupings, not deployables. `internal/engine` is the synchronous
application layer that coordinates them; `cmd/tf-mut` is an adapter. `internal/config`,
`internal/buildinfo`, `internal/buildchain`, and `internal/tracing` are infrastructure outside this map.

## Bounded contexts

The six-context table below names every package grouping and the dependency
policy between them. The depguard rules in `.golangci.yml` enforce the specific
boundaries listed below. `internal/oracle` owns the mutant outcome and every
diagnosis: survivors and terminal outcomes alike are constructed only through
its constructors. Its `Population` carries classified outcomes and execution
provenance from the classification sites to metrics and population refusals.
The engine projects these results onto report DTOs without reconstructing
outcomes from them. Cache rehydration rebuilds a stored verdict through the
oracle's parsing constructor. The judgement points, the input provenance, the
pins, the scaffolds and the scenarios are owned by `internal/characterise`,
which no longer imports `internal/report`.

| Context | Kind | Packages | Owns |
| --- | --- | --- | --- |
| Oracle / Verdict | Core | `internal/fingerprint`, `internal/oracle` | observation, mask, delta, comparison certainty, verdict precedence, mutant outcome, diagnosis, classified population, authority, freshness, metrics |
| Mutation Catalogue | Supporting | `internal/mutation` | operator identity, applicability, site, generated mutant, diff |
| Suggestion | Supporting | `internal/suggest` | candidate to verified/refuted/skipped, verification evidence, apply plan |
| Characterisation | Supporting | `internal/characterise` | scenario, input provenance, judgement point, pin, scaffold promotion, convergence, curation finding |
| Terraform Boundary | Generic / ACL | `internal/discovery`, `internal/tfexec`, `internal/sandbox` | HCL and JSON discovery, canonical configuration snapshot, Terraform process and result translation, sandbox materialisation |
| Publication | Generic / ACL | `internal/report`, `internal/skill` | versioned report DTOs, schema mapping, the seven renderings, shipped agent documents |

## Dependency direction

Core and supporting contexts may read boundary value types — a canonical
`tfexec.RunPayload`, a `discovery.Reach` — and supporting value types such as a
`mutation.Operator`. The policy forbids the boundary from importing core or
supporting contexts, and forbids core, supporting and boundary packages from
importing Publication (`internal/report`, `internal/skill`), the engine or the
CLI. The application layer may import every context and Publication; only the
CLI imports the application layer. `internal/report` is the stdlib-only
publication leaf and has no domain-context imports. `internal/skill`
is grouped under Publication because it owns shipped documents, but since #91 it
writes through Terraform Boundary's shared checked filesystem primitive
(`internal/sandbox.WriteFreshCheckedMode`, as `suggest --apply` and the
characterisation commit do); that narrow infrastructure dependency is an explicit
exception, not a dependency from the report DTO leaf. Publication otherwise
does not import a domain context. The application layer performs the projections
between context values and report DTOs.

Lint enforces nine boundaries, including test files:

- The pre-existing [#89](https://github.com/andrewesweet/tf-mut/issues/89)
  `context-boundary` rule denies `internal/report` imports from all core,
  supporting and Terraform Boundary packages.
- `hclsyntax-boundary` permits `hclsyntax` imports only in `internal/discovery`,
  `internal/mutation`, `internal/characterise` and `internal/config`. Those
  packages retain native parsing and walking — of the module's own bodies
  (`discovery`, `mutation`) and of characterisation's own artefacts; a module's
  expressions are also reachable as `Module.WalkExpressions`, with native
  syntax asked for at the point of use (`NativeExpression`). Other consumers
  read expression facts through discovery.
- `application-boundary` denies `internal/engine` and `cmd/` imports from all
  core, supporting and Terraform Boundary packages.
- `terraform-boundary` denies core and supporting context imports
  (`internal/fingerprint`, `internal/oracle`, `internal/mutation`,
  `internal/suggest`, `internal/characterise`) from `internal/discovery`,
  `internal/tfexec` and `internal/sandbox`; boundary-to-boundary imports stay
  allowed.
- `publication-boundary` denies `internal/report` imports from every package
  outside `internal/report` except `internal/engine` and `cmd/tf-mut` — the
  application layer and the CLI, the only readers of the publication.
- `report-leaf` allows only standard-library imports from `internal/report`,
  with one exception: its external test files may import the exact package
  they test.
- `telemetry-boundary` permits `go.opentelemetry.io` imports only in
  `internal/tracing`, the infrastructure package that projects span attributes
  and owns the exporter; callers carry `tracing.Attribute` values instead.
  The engine's two OTLP wire-decoder test files hold the rule's one recorded exclusion.
- `skill-boundary` denies `internal/skill` imports from all core, supporting
  and Terraform Boundary packages — skill is Publication, and those packages
  import neither half of it (`internal/report` is `context-boundary`'s edge).
  The application layer and the CLI keep their Publication access.
- `skill-leaf` denies `internal/skill` imports of the domain contexts —
  `internal/fingerprint`, `internal/oracle`, `internal/mutation`,
  `internal/suggest`, `internal/characterise`, `internal/discovery` and
  `internal/tfexec`. Its one documented dependency, `internal/sandbox`'s
  checked write primitive (since #91), is the exception, and its absence from
  the deny list is that exception's record.

Core, supporting and boundary imports of `internal/skill`, and skill's own
imports beyond its one documented sandbox exception, are enforced by the
`skill-boundary` and `skill-leaf` rules above.

## Architecture decisions

The rationale for these boundaries is recorded in
[ADR-0001](docs/adr/0001-package-group-contexts-and-lint-enforced-dependencies.md),
[ADR-0002](docs/adr/0002-closed-constructors-and-report-projection.md),
[ADR-0003](docs/adr/0003-synchronous-immutable-result-facts.md) (superseded),
[ADR-0004](docs/adr/0004-expression-boundary-and-atomic-replacement.md), and
[ADR-0005](docs/adr/0005-in-process-domain-event-dispatch.md).

## Glossary

Each entry names its owning context. Wire spellings below are the closed values
declared by `internal/report`; they are not invitations to add aliases.

### Oracle / Verdict

- **Observation:** the canonical Terraform payload captured for comparison.
  `oracle.Observation` bundles run statuses, diagnostics, the validate verdict, the
  masked delta and unknown paths. The engine assembles these measurements and
  fail-closed adapters. `oracle.Judge` applies the verdict precedence in the core
  context.
- **Mask:** a component-granular record of volatility discovered by baseline evidence
  or static scan: volatile components are removed before comparison while stable
  template components remain observable. If volatility cannot be decomposed
  soundly, the path is recorded as undecidable and the comparison is indeterminate,
  not treated as an excluded whole value.
- **Delta:** the masked observable difference between baseline and mutant.
- **Comparison certainty:** the oracle's proof status for equality or difference,
  including the fail-closed indeterminate cases.
- **Mutant outcome / State:** the aggregate verdict for one mutant. `Invalid`,
  `Killed`, `KilledByError`, `Timeout`, `Survived`, `StructurallyUnassertable`,
  `Unobservable`, `NoCoverage`, `Ignored`, and `Pending` are the closed states.
- **Diagnosis:** why a `Survived` mutant survived. `indeterminate-unknown-values`,
  `indeterminate-volatility`, `weak-assertion`, `no-assertion`, and `unasserted` are
  emitted diagnoses. `mock-masked` is withdrawn: its positive case was disproved and
  it has not been emitted since M3; it remains only for report 2.1.0 compatibility.
  See issue [#50](https://github.com/andrewesweet/tf-mut/issues/50).
- **State disambiguation:** in the Oracle / Verdict context, `State` means a mutant's
  aggregate verdict. In the Terraform Boundary, state means Terraform's evaluated
  resource/state payload. These are different concepts even when both appear in a
  report.
- **Selection provenance:** `full` means the whole population was selected, `since`
  means `--since` selected it, and `sample` means `--sample` selected it.
- **Execution provenance:** how this run obtained a selected mutant's outcome or left it
  unobserved.
  The [report schema](docs/schema/report-2.10.0.json) owns the wire vocabulary and counts.
- **Classified population:** outcomes paired with their mutation operators, together
  with execution provenance, including undispatched mutants and whether execution tripped.
- **Scored population:** the selected population whose outcomes contribute to the
  reported metrics under the gate rules.
- **Authoritative population:** a classified population every member of which was
  observed: no mutant timed out, failed to evaluate or remained undispatched. The oracle
  constructs this proof. Curation findings and the until-dry convergence claim require it.
- **Fresh population:** an authoritative population in which no verdict was
  replayed from the cache. The oracle constructs this refinement. The baseline
  writer accepts only one.
- **Metrics:** the derived counts and scores computed from the classified population.

### Mutation Catalogue

- **Operator identity:** the stable name of a mutation operator.
- **Applicability:** the operator shapes and evidence that permit generation.
- **Site:** the owned source location at which an operator generates a mutant.
- **Generated mutant:** a content-derived mutation candidate.
- **Diff:** the source change owned by a generated mutant.
- **Pack entry:** data, not an operator — a `(resource_type, attribute, form, from, to)`
  record under an author-supplied label that parameterises one form operator, loaded from a
  pack file under the normative contract and selected by pack name.
- **Witnessed entry:** a pack entry that produced bytes for at least one mutant in an
  admission measurement's published output, counted pre-deduplication (bytes produced) and
  post-deduplication (carries an origin on a row), both published. Only witnessed entries
  ship in a seeded pack.
- **Origin:** the `(operator, pack, entry)` whose rewrite produced a generated mutant's
  bytes; aggregated onto the deduplication survivor, sorted and deduplicated by
  `(pack, entry)`, and never part of the mutant's identity.

### Suggestion

- **Suggestion status:** `candidate`, `verified`, `refuted`, `skipped-sensitive`,
  `skipped-unaddressable`, `skipped-unrenderable`, and
  `skipped-unsupported-target`.
- **Verification evidence:** the baseline and isolated-mutant verification legs.
- **Apply plan:** the digest-bound set of verified suggestions and target files to
  write.

### Characterisation

- **Pin:** one harvested value for a scenario and Terraform address, rendered as
  an assertion at the selected granularity or recorded with a closed reason for
  skipping it; skipped pins carry no executable expression.
- **Scaffold promotion:** the transition of non-executable scaffold material into
  test content after its answers and generated behaviour have been verified green.
- **Pin status:** `pinned`, `skipped-sensitive`, `skipped-unrenderable`,
  `skipped-volatile`, and `skipped-mock-invented`.
- **TODO status:** `open`, `answered`, `promoted`, and `rejected`.
- **Scaffold status:** `scaffolded` and `promoted`.
- **Curation finding:** an evidence-bearing report of redundant or ineffective
  assertions over an authoritative, fully observed population.
- **Curate finding kind:** `empty-kill-set`, `subsumed`, and
  `cross-scenario-redundant`.
- **Assertion provenance:** `generated-unmodified`, `generated-edited`, and
  `pre-existing`.
- **Input provenance:** `default` (the variable default), `mined` (a validation
  condition), `typed` (the declared type), and `answered` (a TODO answer).
- **Scenario:** an input assignment set and isolated Terraform state key used for
  harvesting.
- **Judgement point:** a non-executable TODO where deterministic synthesis needs a
  human answer.
- **Convergence:** the until-dry evidence that records rounds, newly pinned values,
  and the stop reason.

### Terraform Boundary

- **Terraform state:** Terraform's evaluated resource and state payload. It is not
  the Oracle's mutant `State`.
- **Configuration snapshot:** the canonical closure supplied to Terraform.
- **Discovery:** parsing and inventory of native HCL and discover-only JSON.
- **Materialisation:** creation of a sandbox with fresh inodes for Terraform.
- **Result translation:** conversion of Terraform process output and diagnostics at
  the boundary.

### Publication

- **Schema mapping:** a renderer's projection from the authoritative report value
  into a versioned external schema or interoperability dialect; mappings may be
  explicitly lossy, while tf-mut's report metrics remain authoritative.
- **Report DTO:** the versioned value returned by the engine and consumed by
  renderers. The JSON contract is linked in the [README document map](README.md#documents).
- **Command:** a report-producing invocation: `run`, `preview`, `suggest`,
  `characterise`, `curate`, or `todos`.
- **Renderer:** one of the seven report projections: terminal, JSON, SARIF,
  mutation-testing-elements, HTML, JUnit, and Markdown.
- **Shipped agent document:** a skill or other installed document emitted by
  `internal/skill`.
