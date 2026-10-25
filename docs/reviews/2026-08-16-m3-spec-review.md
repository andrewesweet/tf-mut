# M3 spec review — 16 August 2026

Adversarial review of the M3 milestone spec (issue #33, first body), posted as a comment on
that issue. **Verdict as delivered: "no, this is not ready for implementation."** The review
endorsed the two up-front decisions (mock-masked prove-or-withdraw; C4-clause retirement) and
found "several routes to false green results" in the rest.

All fifteen findings accepted, three with modification. Issue #33's body after this review
(revision 2) and the design corrections in the same change are the disposition of record.

## Dispositions

| # | Sev | Finding (compressed) | Disposition |
| --- | --- | --- | --- |
| C1 | CRIT | Conditional `NoCoverage` evaluates the **original** program: `count = var.enabled ? 1 : 0` under all-false runs pre-classifies the block, suppressing exactly the execution that would reveal a `COND-SWAP` mutant in (or upstream of) the condition — the R2-1 error class again | **Accepted.** Pre-classification is per-**mutant**, never per-block: the **mutated** multiplicity expression must be statically zero under every relevant run, and any mutant whose site is in or graph-upstream of the multiplicity expression always executes. The evaluator's input context and supported expression forms are now enumerated, everything else failing closed to execution; the reviewer's three acceptance cases (body-only stays `NoCoverage`; in-condition executes; upstream executes) are mandatory |
| C2 | CRIT | Static `Unobservable` contradicts the settled precedence: validations/pre/postconditions have no projection path but are `StructurallyUnassertable` (or killable via `expect_failures`), never `Unobservable` — M2-12 already established the structural state comes first | **Accepted.** Structural-state precedence applies before any graph shortcut; static `Unobservable` is restricted to projecting expression classes; the M2 `contract` fixture must classify identically with the shortcut enabled |
| C3 | CRIT | Resource-level `terraform graph` agreement cannot establish attribute-level soundness for the equality claim; the misses live exactly in locals/outputs/wiring/collections/splats and the HCL-address-to-payload-path mapping | **Accepted.** Revision 2 specifies one canonical address model (module path with conservative instance wildcards, resource address, attribute segments; splat/wildcard = match-all) shared by graph nodes and payload paths, with **fail-closed adapters at both ends**: a mutant site that does not map to a graph node falls back to the whole-payload unknown rule for that mutant; a payload unknown that does not map into the graph is treated as in-cone. Exercised over every operator generation site. The `terraform graph` comparison is demoted to a supplemental test-suite check, never evidence of attribute-level soundness |
| C4 | CRIT | The cache key omits verdict-changing inputs (rest of the module closure, masked baseline fingerprint, remote payloads, auto-var files, `TF_VAR_*`); cached evidence can go stale invisibly | **Accepted.** The coarse correct key ships first: hash of the entire materialised source closure, all tests, resolved configuration, lock and module inventory, relevant environment, Terraform identity, oracle/cache format version, and the masked baseline fingerprint. Source metadata is rehydrated, not trusted from cache. Cache reads and writes are disabled under `--allow-real-infrastructure` and `--allow-unsandboxed-effects`. Finer graph-derived invalidation waits for a measurement proving the coarse key insufficient — standing rule 1 applied to a cache |
| C5 | CRIT | `git diff <ref>...HEAD` excludes staged, unstaged and untracked changes: an uncommitted new resource gets an empty, fast, green `--since` run — the inverse of the inner-loop story | **Accepted.** Selection is the union of the committed range, the working tree, and untracked Terraform/test/configuration files; rename, deletion, conflict, shallow-clone and missing-ref behaviour specified; "changed configuration" defined as an exact file-class list |
| C6 | CRIT | The Stryker mapping changes tf-mut's scores three ways (`KilledByError`→RuntimeError drops a numerator; Timeout counts as detected; `StructurallyUnassertable`→Ignored drops a denominator), and the closed status vocabulary permits no lossless escape | **Accepted with modification.** The mutation-testing-elements reporter is retained as an **explicitly lossy interoperability adapter**: schema version pinned, tf-mut's authoritative metrics embedded in the report's metadata and rendered above the viewer in the HTML page, the score disagreement **tested rather than denied**, and every claim of unchanged-dashboard-score reuse withdrawn |
| C7 | CRIT | Signature compatibility yields 2,219 unordered pairs (525 after canonicalising the 119 `core::` aliases; the unary string→string group alone is 406), mostly non-fault-realistic (`file`→`upper`) — hundreds of new mutants in the milestone trying to reduce the population | **Accepted.** No Cartesian product. Generation canonicalises aliases and emits only explicitly justified semantic families extending the curated M2 set. The generated catalogue moves to the **last** sub-scope, ships behind an opt-in flag rather than `standard`, and may widen `standard` only after published candidate counts, generated-mutant counts and per-pair invalid/error rates on a real module — a measured fault model, per standing rule 1 |
| C8 | CRIT | Baseline/sampling/scoped-run gate semantics lack a truth table: unobserved ≠ stale, a sampled run can trivially satisfy `--fail-on-new`, state transitions undefined, `--min-score`/`--fail-on-new` composition undefined | **Accepted.** Revision 2 carries a normative gate table: staleness and baseline rewrite require a full unsampled population; sampled runs are non-authoritative and cannot satisfy `--min-score` or `--fail-on-new` without a separately named unsafe opt-in; a baseline entry absent from a scoped population is **unobserved**; any current finding (state or actionability class) not accepted in the baseline is new — a previously-`Killed` ID surviving is new, an accepted indeterminate turning actionable is new; selected, omitted, cached and fresh populations reported distinctly |
| M1 | MAJ | The `terraform graph` gate's execution location is contradictory (runtime per the spec, test-only per the design's original posture) | **Accepted.** Test-suite-only, matching the design's original reasoning; user runs never invoke `terraform graph`; the spec states the fixture diversity that makes the check meaningful |
| M2 | MAJ | Cone prioritisation has no consumer (no `--fail-fast`, no live reporting — reordering changes nothing observable), and the whole-object-read story adds no capability (`Evidence.Delta` already names the changed attribute) | **Accepted.** Prioritisation is **deleted** from M3 — dead complexity; it returns only with a specified consumer. M2-review open question 3 is re-disposed as **no change needed**: the delta already carries the attribute; the graph's diagnosis role is confined to `weak-assertion`/`no-assertion` reachability soundness |
| M3 | MAJ | Reporter contracts incomplete: JUnit's full state mapping and dialect unnamed; the new report facts (cache provenance, sampling, baseline states, populations) need a named schema version; HTML self-containment and licensing unstated | **Accepted.** Schema `report-2.1.0` (additive) enumerates the new fields; JUnit maps every state, against the vendored Jenkins JUnit dialect schema; HTML is self-contained and offline with the embedded mutation-testing-elements version pinned and its licence shipped |
| M4 | MAJ | The Action lacks a trust and failure-order contract: SARIF upload needs `security-events: write`, comments need write, fork PRs get read-only tokens, a failing run step stops later composite steps, and "pin by checksum" has no distribution contract | **Accepted.** Revision 2 specifies: capture-run → upload → comment → final-exit ordering with the documented non-blocking pattern; least-privilege caller permissions listed; fork/Dependabot degradation (skip uploads, keep the check outcome); `pull_request_target` over untrusted checkout forbidden; a distribution contract (release assets, architecture, checksum source, verification-failure behaviour, caller-supplied version). "Never requires cloud credentials" narrowed to infrastructure-provider credentials |
| M5 | MAJ | The speed gate can pass without proving speed: no pinned diff, expected mutant count, cache state or protocol; an absolute laptop bound is not a portable gate | **Accepted.** The gate pins fixture commit, exact diff, expected selected IDs and a non-zero minimum, cache-off state, tier and warm/cold protocol; asserts shared-verdict identity; publishes hardware and method; the portable regression assertion is selected-count plus factor-versus-baseline, with the 60-second figure a published measurement on named hardware, not a portable assertion. Product claim narrowed: a code-plus-test PR forces the full population and is not sub-minute |
| M6 | MAJ | Persistent cache safety unspecified: evidence embeds plan values and source; a shared disk format needs permissions, atomicity, corruption behaviour, locking, traversal protection, eviction and a sensitive-value statement | **Accepted.** All specified in revision 2: project-local default location, `0700`, atomic rename writes, corruption-as-miss, advisory locking, no symlink traversal, deterministic eviction, and an explicit statement that evidence may embed plan values — with CI cache-restore risk documented and `--no-cache` the stated mitigation |
| M7 | MAJ | Breadth is sequenced parallel to the truth gates: reporters and the Action can publish unproven verdicts, and the generated catalogue grows the population before the count levers are measured | **Accepted.** The sub-scopes are now **ordered**: graph/address soundness first, then the levers and their gate semantics, then the real-provider gate, then reporters and the Action, and the generated catalogue last behind its measurement gate. Later sub-scopes are blocked on the earlier gates, not merely unable to close the milestone |

## Pattern note

Fifth review, and the recurring shape is now unmistakable: **every unsound proposal in this
spec was a shortcut evaluated against the wrong program** — the original module instead of
the mutant (C1), the resource graph instead of the attribute graph (C3), the committed tree
instead of the working tree (C5), the block instead of the finding (C8). The repair is the
same every time: state what is being proven, about which artefact, and fail closed where the
mapping is missing. That sentence is now the checklist question for any future shortcut:
*which program is this evaluated against, and what happens where the mapping fails?*

## Addendum — 25 October 2026, child-environment cache key

Measurement (the child-environment design review at
`/home/andre/tools/firstmate/data/tfmut-cache-key-design/report.md`,
recorded here because it amends a disposition, not because the report is a project document)
showed the C4 environment clause missing on every agent tool call and every systemd unit:
three cross-session runs missed on 315 of 315 mutants each, and the only names that changed
between agent calls were `TRACEPARENT`, `OLDPWD` and `TF_MUT_DEBUG`. The clause below is
**amended**; the rest of C4 stands unchanged.

Superseded, exactly: the environment clause of the C4 disposition above — "hash of the
entire materialised source closure, all tests, resolved configuration, lock and module
inventory, relevant environment, Terraform identity, oracle/cache format version, and the
masked baseline fingerprint" — and its round-two restatement that "the cache keys the whole
effective environment, no allowlist"
(`docs/research/10-m3-exit-gate.md:131-132`). Kept: "any doubt is a miss".

The amended rule: the key hashes the **child environment** — the exact environment tf-mut
hands a Terraform process — computed once at the `tfexec` seam
(`internal/tfexec/tfexec.go:470`, assigned at `internal/tfexec/stream.go:41`), together with
tf-mut's own verdict-relevant in-process reads. Those reads are `TF_VAR_*`
(`internal/discovery/multiplicity.go:492`, `internal/discovery/suitesupplied.go:113`) and
`TF_CLI_ARGS_test` (`internal/discovery/suitesupplied.go:129`): both are forwarded into the
child, so keying the child keys them, and neither may ever enter the strip set. `--since`'s
git environment (`internal/engine/since.go:565`) selects mutants but judges none, and the
OTLP endpoint (`internal/tracing/tracing.go:105`) changes no verdict; both stay keyed by
over-inclusion, which costs only misses. `TF_MUT_DEBUG` is read only by the CLI for logging
(`cmd/tf-mut/main.go:709`) and must not be a miss.

A variable the key leaves out is a variable no Terraform process, provider or git subprocess
ever receives, so a hit cannot replay a verdict a change in that variable would alter. The
risk moves from silent to operational: a provider or git needing a stripped name fails
visibly through the baseline gate, never as a stale verdict.

Reviewed strip set, one line of evidence per name (grades: **Direct** = measured or read here
or in the cited review; **Supported** = same-family inference from a Direct read; every name
also has zero reads in tf-mut's non-test code, verified by grep on master at amendment time):

| Name(s) | Evidence |
| --- | --- |
| `GIT_DIR`, `GIT_WORK_TREE`, `GIT_INDEX_FILE`, `GIT_OBJECT_DIRECTORY`, `GIT_ALTERNATE_OBJECT_DIRECTORIES`, `GIT_COMMON_DIR`, `GIT_NAMESPACE`, `GIT_PREFIX` | Pre-existing: an inherited location variable turns a remote-module download into "fatal: working tree already exists" (`internal/tfexec/tfexec.go:35-43`). **Direct.** |
| `TRACEPARENT` | Terraform v1.15.8 ignores an inherited one: a local OTLP receiver accepted 6 `/v1/traces` posts whose trace IDs never contain the inherited value, and the binary holds no `TRACEPARENT` literal. **Direct.** |
| `TRACESTATE` | W3C companion of `TRACEPARENT`, meaningless without it; Terraform propagates no trace context once `TRACEPARENT` is not propagated. **Supported.** |
| `SHLVL`, `OLDPWD`, `_` | Shell bookkeeping (nesting depth, previous directory, last-command path); no non-test `Getenv`/`LookupEnv` in Terraform v1.15.8 core. **Direct** (source read in the design review). |
| `TMUX`, `TMUX_PANE` | Multiplexer socket identity; `TMUX` covered by the same source read, `TMUX_PANE` its per-pane companion. **Direct** / **Supported.** |
| `TERM_PROGRAM`, `TERM_PROGRAM_VERSION` | Terminal emulator identity; `TERM_PROGRAM` covered by the same source read, the version its companion. **Direct** / **Supported.** |
| `INVOCATION_ID`, `SYSTEMD_EXEC_PID` | systemd per-invocation identifiers; covered by the same source read. **Direct.** |
| `MEMORY_PRESSURE_WATCH`, `MEMORY_PRESSURE_WRITE` | cgroup pressure notification sockets; `MEMORY_PRESSURE_WATCH` covered by the same source read, the write end its companion. **Direct** / **Supported.** |
| `JOURNAL_STREAM` | Journal stream identifier, same per-invocation family as the systemd names above. **Supported.** |
| `SSH_CLIENT`, `SSH_CONNECTION`, `SSH_TTY` | SSH session identifiers; covered by the same source read. **Direct.** |
| `XDG_SESSION_ID` | Login session identifier; covered by the same source read. **Direct.** |
| `TF_MUT_*` | tf-mut's own namespace: `TF_MUT_DEBUG` is CLI logging only; test markers travel via run additions, which stay forwarded. **Direct** (code read). |

Deliberately forwarded and keyed: `PATH` and `SSH_AUTH_SOCK` (they locate and authenticate
subprocesses; standing rule 5 keeps those), `TERM`, `COLORTERM` and `PWD` (no measured hit
depends on them), and every `TF_*` name outside `TF_MUT_*` — above all `TF_VAR_*`, whose
demonstrated false reuse (`TF_VAR_strict=false` turning 3 Killed into 3 Survived) rejects any
exclusion by example.

Rejected alongside: a key-side denylist (key and child disagree by design), a reader
allowlist (unbounded third-party provider reads), and a user-declared ignore list (one wrong
entry replays wrong Killed verdicts). The 32-name miss-explanation cap
(`internal/report/report.go:353`) is untouched and noted as a follow-up.

Acceptance carries the M4 spec review M5 rule: every recorded transition replayed against
the new key with a published false-reuse count of 0, an engine-seam test proving a stripped
variable cannot change a verdict, an engine-seam test proving `TF_VAR_*` set-then-absent
still misses, and the miss explanation still naming forwarded names.
