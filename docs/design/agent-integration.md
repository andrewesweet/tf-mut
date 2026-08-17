# Failure modes, and design for agent drivability

Two questions are answered here. First, where does the characterisation approach (and the
mutation loop it rests on) actually fail? Second, given that some of those failures are
resolvable only by judgement — and the tool itself will **never make an LLM call** — how is it
designed so that a coding agent driving it can supply that judgement instead?

The division of labour this document designs for:

> **The tool is the oracle and the actuator. The agent is the judgement.**
> Everything deterministic — parsing, scaffolding, execution, verification, measurement —
> lives in the tool. Everything requiring understanding of intent lives outside it. The tool's
> job is to make every judgement point *legible* and every proposed answer *verifiable*.

This inverts the usual trust relationship with coding agents: the agent is creative and
fallible, the tool is judicial and deterministic. An agent-supplied value is never trusted —
it is planned, harvested, and measured like anything else. Agent errors become tool findings.

---

## 1. Failure-mode taxonomy

Each failure mode lists how the tool detects it, what it does, and who can actually resolve
it: **T** = the tool alone, **A** = a driving agent, **H** = a human. Modes marked A are the
design targets for §2.

### A. Severing and environment failures

| # | Mode | Detection | Tool response | Resolver |
| --- | --- | --- | --- | --- |
| A1 | Unseverable seams: `external` and `http` data sources, provisioners, `terraform_remote_state` | AST scan at discover time | Report as `unseverable` with source range and reason — and **refuse apply-mode execution without `--allow-unsandboxed-effects`** (R2-10: provisioners execute under mocked apply; a `local-exec` verified firing with no real provider present, so exclusion-from-pinning alone is not safety). Plan-mode characterisation of the severable remainder proceeds | A (restructure: wrap in a mockable data source, gate with a variable) or H |
| A2 | Module cannot `init` offline: private registry, git-over-SSH module sources | `init` failure, structured diagnostic | Fail fast, name the source address | H (credentials) — not an agent problem |
| A3 | Terraform version below 1.6/1.7: no test framework or no mocking | `version -json` at discover | Fail fast with the minimum-version statement | H |
| A4 | Nondeterminism providers (`random_*`, `time_*`) unmocked: fresh state per run ⇒ new values per run ⇒ volatile | Double-run volatile mask catches it; AST scan predicts it | Auto-generate `mock_provider "random"` / `"time"` blocks with pinned values — these providers have full schemas like any other | T |
| A5 | Mock-vs-reality gap: real providers apply server-side defaults, normalisation, validation that mocks do not | Cannot be detected locally, by construction | Stated in every characterisation report header: the suite pins *Terraform-side* semantics only. This is a scope boundary, not a bug | — (inherent) |

### B. Input synthesis failures — the heuristic core

| # | Mode | Detection | Tool response | Resolver |
| --- | --- | --- | --- | --- |
| B1 | Validation condition too complex to mine (regex, `can()` chains, cross-references) | Mining returns nothing; synthesised value fails the validation at plan | Emit a structured TODO carrying the full validation expression, its source range, and the failing diagnostic | **A** — reading `regex("^ami-[0-9a-f]{8}", var.x)` and producing `"ami-12345678"` is exactly what an agent is good at and a tool is not |
| B2 | Cross-variable constraints (`precondition` over two variables): satisfying assignment is constraint solving | Plan-time failure referencing multiple variable traversals | TODO with all involved variables and the constraint expression | **A** |
| B3 | Function domain errors on synthesised values (`cidrsubnet`, `formatdate`, `jsondecode`) | Plan diagnostic with source range | Repair table for common shapes (CIDR, ARN, region, JSON) first; TODO with the diagnostic on miss | T first, then **A** |
| B4 | Structurally valid, semantically absurd synthesis: deep objects filled with placeholders plan successfully and pin meaningless behaviour | *Not mechanically detectable* — the plan succeeds | Report synthesised inputs prominently per run block, flagged `synthesised`, so review is possible; never bury them | **A** review, or accepted as-is (a characterisation of the module under odd inputs is still a regression net) |
| B5 | Ephemeral/sensitive variables (v1.10+): values must not be pinned into test files | AST: `ephemeral`/`sensitive` markers | Synthesise into `variables` blocks but never into assertions; note in report | T |

### C. Harvest and pinning failures

| # | Mode | Detection | Tool response | Resolver |
| --- | --- | --- | --- | --- |
| C1 | Volatility beyond the double-run mask: values varying per-mutant rather than per-run (e.g. `plantimestamp()` reached only under some mutants) | Fingerprint noise localised to specific attributes across mutant runs | Extend the volatile mask incrementally as noise is observed; re-verify affected verdicts | T |
| C2 | Zero-output modules: `outputs`-level characterisation pins nothing and reports false confidence | Trivially countable | Refuse to report success at `outputs` level when the output count is 0; auto-escalate to `counts` and say so | T |
| C3 | Create-path-only characterisation: the suite pins behaviour from empty state; in-place update and replace semantics (day-2 behaviour) are invisible | Inherent to single-run scaffolds | Stated limitation. Sequential two-run scaffolds (apply, mutate an input, apply again) are mechanically generatable, but *which* input changes model realistic day-2 drift is judgement | **A** proposes update scenarios; tool generates, harvests and verifies them like any other run |
| C4 | Over-pinning brittleness: `configured`-level suites fail on every benign refactor and get deleted by annoyed humans | Social, not technical | Conservative default (`outputs`), granularity ladder, `curate` for pruning | T + H |

### D. Curation failures

| # | Mode | Detection | Tool response | Resolver |
| --- | --- | --- | --- | --- |
| D1 | Set-cover pruning overfits the operator catalogue: an assertion kills nothing *under current mutants* but guards a fault class the catalogue does not model | Inherent — the kill-set is only as rich as the catalogue | `curate` **reports; it never auto-deletes.** Pruning requires an explicit apply, and the report says which operators the retained evidence comes from | **A**/H judge each prune |
| D2 | `--until-dry` oscillation: each pinned assertion perturbs kill-sets; loop may not reach a fixed point | Iteration counter | Bounded iterations (default 5) with a convergence report | T |
| D3 | Score plateau read as completeness: survivors classified `unobservable-under-current-inputs` mean the *inputs* are the gap, not the assertions | Already a distinct diagnosis | The until-dry loop surfaces these as "new run block needed", with the mutation site and the inputs every existing run supplied | **A** proposes the discriminating input; tool verifies it actually changes the fingerprint |

The pattern across every **A** row is identical: the tool detects the gap precisely, packages
the evidence, and can verify any proposed answer — it just cannot invent the answer. That is
the interface to design.

---

## 2. Agent drivability — the tool as a substrate

Constraints: the tool makes no LLM calls, ever; it must also never *require* an agent — every
affordance below degrades to a human running the same commands.

### 2.1 Machine-readable everything

- Every command takes `--format json` with a **versioned schema** (`"schema_version"` in every
  document). Exit codes are stable and documented.
- Every entity has a **stable, content-derived ID**: mutants (`mut-<hash>`), TODOs
  (`todo-<hash>`), assertions (`asrt-<hash>`), run blocks. IDs survive re-runs and unrelated
  edits, so an agent can hold a reference across a session.
- **Token-frugal output is a design requirement, not a nicety.** Agent context is the scarce
  resource. Defaults: summaries with counts, `--top N` for lists, `explain <id>` for depth on
  one entity, plan deltas rendered as *diffs against baseline* rather than full documents, and
  `provider_schemas`-scale payloads never emitted to stdout. A driving agent should be able to
  run the whole loop reading a few KB per iteration.

### 2.2 The TODO protocol

Where synthesis fails (B1–B3), the generated test file carries a structured marker, and the
value is a placeholder that fails loudly rather than a guess that passes silently:

```hcl
run "characterise_defaults" {
  variables {
    # tf-mut:todo todo-9f2a kind=input-synthesis
    vpc_cidr = "TFMUT_TODO"   # fails validation: must match ^10\\..* (main.tf:12)
  }
}
```

`tf-mut todos --format json` lists every open TODO with its full evidence bundle: the
constraint expression verbatim, its source range, the diagnostic from the last attempt, the
values already tried, and — where mining partially succeeded — the candidates considered. The
agent's move is ordinary file editing: replace the placeholder, re-run
`tf-mut characterise --resume`. The tool then *verifies* the value (plan succeeds, validation
passes, fingerprint stable) and closes or re-opens the TODO. No special channel: the interface
is the file system plus a JSON report, which every coding agent already speaks.

### 2.3 Decision points as declarative inputs, never prompts

The tool is never interactive. Every judgement point is expressible as configuration, so an
agent supplies decisions the same way a CI file would:

| Decision | Mechanism |
| --- | --- |
| TODO answers | Edit the file (preferred), or `--answer todo-9f2a='10.0.0.0/16'` for scripted runs |
| Granularity per run block | `--pin outputs\|counts\|configured`, overridable per run in `.tf-mut.hcl` |
| Day-2 scenarios (C3) | An agent writes an ordinary second run block; the tool harvests and pins it like its own |
| Prune approvals (D1) | `tf-mut curate --apply asrt-3c41,asrt-77b0` — explicit IDs only, no `--apply-all` |
| Discriminating inputs (D3) | An agent adds a run block with the proposed inputs; the tool reports whether it changed the fingerprint, i.e. whether the proposal actually discriminates |

### 2.4 The verification loop is the safety property

Nothing an agent supplies is trusted. A TODO answer is re-planned; a proposed day-2 scenario is
harvested and its assertions are generated from *observed* state, not from the agent's claim; a
pruned assertion's kill-set is recomputed before and after. A hallucinated CIDR fails the plan
and re-opens the TODO with a fresh diagnostic — the failure mode of agent error is a *reported,
attributed finding*, never a corrupted suite. This property is what makes it safe to hand the
loop to an agent at all, and it costs nothing: it is the same machinery that verifies the
tool's own heuristics.

### 2.5 Why not MCP-first

The substrate is the CLI with JSON output, deliberately. It works identically for humans,
shell scripts, CI, and every agent framework; it is testable with `jq`; and it forces the
token-frugality discipline of §2.1. An MCP server wrapping the same commands is a thin,
optional layer — post-MVP, if demand exists. The design rule: **nothing may be reachable via
MCP that is not reachable via the CLI.**

---

## 3. Agent skills — packaged, installed, evaluated

A skill is the missing half of agent drivability: the tool provides affordances, the skill
teaches the loop. Two skills ship with the tool, and installation is a tool subcommand so the
skill version always matches the binary:

```
tf-mut skill install [--agent claude|generic] [--path .]
# generic serves Cursor and every other agent framework; a dedicated cursor
# adapter ships only if the generic form proves insufficient (M4 spec review, M4)
```

- **`tf-mut-mutation`** — the grading loop. When to run `coverage` first, how to read the
  three metrics together, the survivor-diagnosis decision tree (which diagnoses mean "add a
  run block" versus "add an assertion" versus "suppress with a reason"), how to apply
  `suggest` output, and the discipline of `--since` + baseline in PR workflows.
- **`tf-mut-characterise`** — the scaffolding loop. Run `characterise`, drain `todos` (with
  the B1/B2 reasoning patterns spelled out: read the validation expression, produce a
  conforming value, never weaken the constraint), propose day-2 run blocks, drive
  `--until-dry`, review `curate` findings against intent before applying any prune.

Each skill encodes the judgement patterns of §1's **A** rows as instructions, keeps the tool's
JSON as its only data source, and instructs the agent to let the tool verify every step —
the skill is explicitly forbidden from having the agent hand-write assertions the harvest can
generate, because harvested assertions are evidence and hand-written ones are guesses.

### Milestones

Per the roadmap in `product-design.md`:

- **End of MVP scope.** Skill authoring and `skill install` for both loops: the mutation skill
  lands with M4 (it needs `suggest` to be teachable), the characterisation skill with M4.5.
  MVP is not done until an agent can drive both loops end-to-end from the shipped skills.
- **Post-MVP.** Empirical evaluation and optimisation: benchmark agent-with-skill against
  agent-without-skill on a public legacy-module corpus, measuring time-to-first-green-suite,
  final mutation score, TODO-resolution rate, prune precision, and tokens consumed. Treat the
  skill text itself as the variable under optimisation — the same measure-then-improve loop
  the tool applies to test suites, applied to its own documentation.

---

## 4. Summary

The heuristics are not a flaw to engineer away; they mark the genuine judgement content of the
problem. The design response is to make the tool a **deterministic substrate with legible
judgement points**: every gap detected precisely, every gap addressable through files and
flags, every proposed answer verified by execution. An agent supplies reasoning; the tool
supplies ground truth; and the failure mode of bad reasoning is a clean finding rather than a
corrupted result. No LLM call ever originates from the tool.
