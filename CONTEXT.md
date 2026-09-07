# tf-mut context

This repository is one Go module and one documented context. The bounded contexts
below are package groupings, not deployables. `internal/engine` is the synchronous
application layer that coordinates them; `cmd/tf-mut` is an adapter. `internal/config`,
`internal/buildinfo`, and `internal/buildchain` are infrastructure outside this map.

## Bounded contexts

The six-context table and dependency direction below are the target architecture
specified by #86, not a claim that the current packages already satisfy every
boundary. `internal/oracle` does not exist yet, and verdict extraction is pending
#101–#103. Supporting-context migrations and dependency enforcement remain pending
in #89, #105–#109 and #113; #91 is the separately recorded narrow
filesystem-primitive exception. Until those tickets land, current imports and
ownership remain as implemented while this document states the normative direction.

| Context | Kind | Packages | Owns |
| --- | --- | --- | --- |
| Oracle / Verdict | Core | `internal/fingerprint`, `internal/oracle` | observation, mask, delta, comparison certainty, mutant outcome, diagnosis, scored population, metrics |
| Mutation Catalogue | Supporting | `internal/mutation` | operator identity, applicability, site, generated mutant, diff |
| Suggestion | Supporting | `internal/suggest` | candidate to verified/refuted/skipped, verification evidence, apply plan |
| Characterisation | Supporting | `internal/characterise` | scenario, input provenance, judgement point, pin, scaffold promotion, convergence, curation finding |
| Terraform Boundary | Generic / ACL | `internal/discovery`, `internal/tfexec`, `internal/sandbox` | HCL and JSON discovery, canonical configuration snapshot, Terraform process and result translation, sandbox materialisation |
| Publication | Generic / ACL | `internal/report`, `internal/skill` | versioned report DTOs, schema mapping, the seven renderings, shipped agent documents |

## Dependency direction (target)

Core and supporting contexts, and every Terraform Boundary package, do not import
Publication (`internal/report`), the CLI, or a renderer. `internal/report` is the
stdlib-only publication leaf and has no domain-context imports. `internal/skill` is
grouped under Publication because it owns shipped documents, but the planned #91
implementation may use Terraform Boundary's shared checked filesystem primitive
(`internal/sandbox.WriteFreshChecked`); that narrow infrastructure dependency is an
explicit exception, not a dependency from the report DTO leaf. Publication otherwise
does not import a domain context. The application layer may import every context and
performs the projections between context values and report DTOs.

## Architecture decisions

The rationale for the target boundaries and future migrations is recorded in
[ADR-0001](docs/adr/0001-package-group-contexts-and-lint-enforced-dependencies.md),
[ADR-0002](docs/adr/0002-closed-constructors-and-report-projection.md),
[ADR-0003](docs/adr/0003-synchronous-immutable-result-facts.md), and
[ADR-0004](docs/adr/0004-expression-boundary-and-atomic-replacement.md).

## Glossary

Each entry names its owning context. Wire spellings below are the closed values
currently declared by `internal/report`; they are not invitations to add aliases.

### Oracle / Verdict

- **Observation:** the canonical Terraform payload captured for comparison.
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
- **Execution provenance:** `fresh` means this run computed the verdict; `cached`
  means the verdict was replayed from the incremental cache with evidence rehydrated.
- **Scored population:** the selected population whose outcomes contribute to the
  reported metrics under the gate rules.
- **Metrics:** the derived counts and scores computed from the classified population.

### Mutation Catalogue

- **Operator identity:** the stable name of a mutation operator.
- **Applicability:** the operator shapes and evidence that permit generation.
- **Site:** the owned source location at which an operator generates a mutant.
- **Generated mutant:** a content-derived mutation candidate.
- **Diff:** the source change owned by a generated mutant.

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
  renderers. Its current schema version is `2.3.0`.
- **Command:** a report-producing invocation: `run`, `preview`, `suggest`,
  `characterise`, `curate`, or `todos`.
- **Renderer:** one of the seven report projections: terminal, JSON, SARIF,
  mutation-testing-elements, HTML, JUnit, and Markdown.
- **Shipped agent document:** a skill or other installed document emitted by
  `internal/skill`.
