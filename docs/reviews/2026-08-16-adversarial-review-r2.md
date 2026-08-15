# Adversarial review R2 — 16 August 2026

Second independent adversarial review, this time by an external agent, over the revisions
made in response to round one. Delivered as
[issue #1](https://github.com/andrewesweet/tf-mut/issues/1); full report preserved there.
Target: `master` @ `9a19568`. The reviewer ran its own experiments against Terraform
v1.15.8.

**Verdict as delivered:** "No. I would not fund implementation of the revised design yet…
I would fund a short third design/spike round whose exit criteria are reproductions for
R2-1 through R2-7 and corrected exclusive metrics; those are bounded repairs, but they sit
below every later differentiator and must be resolved before implementation."

All thirteen findings were accepted, three with modification. Five of them (R2-1, R2-4,
R2-5, R2-11, R2-13) attack repairs made in round one — the review-the-revisions instruction
earned its keep.

## Dispositions

| # | Sev | Finding (compressed) | Disposition |
| --- | --- | --- | --- |
| R2-1 | CRIT | Assertion-cone **exclusion** deletes the product's primary finding class: a planned-but-never-asserted resource has an empty assertion cone, so selection would run nothing — no fingerprint, no `no-assertion` diagnosis, no suggestion, and characterisation's assertion-less suites select nothing at all | **Accepted.** Selection basis inverted: a run is selected iff it **instantiates/evaluates** the mutated block (plan reachability). The assertion cone prioritises and predicts diagnoses; it is never an exclusion rule |
| R2-2 | CRIT | Plan-JSON fingerprint equality does not prove unassertability: cty **unknown-value refinements** (e.g. the known prefix of `"stable-${unknown}"`) are retained by the assertion evaluator but discarded by `test_plan` serialisation — verified with a `startswith` assertion killing a fingerprint-identical mutant | **Accepted.** The soundness claim is restricted: `Unobservable` may only be assigned when no value in the mutant's evaluated paths is unknown. Fingerprint-identical mutants with unknowns in scope classify `Survived` with diagnosis `indeterminate-unknown-values`, and the suggestion engine is barred from claiming impossibility for them |
| R2-3 | CRIT | Hardlinked sandbox files share the source inode; `os.WriteFile` truncates in place and **mutates the source tree** — the exact drift the design calls structurally impossible | **Accepted.** Sandbox spec: hardlinks only for files that will not be written; the mutated file is always a fresh inode (write-temp + atomic rename), and the writer asserts the target inode differs from the source (or `st_nlink == 1`) before writing |
| R2-4 | CRIT | One-run-per-file splitting changes Terraform's **implicit shared-state** semantics: runs against the same module share in-memory state with no `state_key` and no `run.<name>` reference — verified with an apply→plan pair whose extracted plan run fails alone | **Accepted.** The split closure is state-identity-aware: every preceding state-producing (apply) run sharing the mutated module's state identity joins the prefix. Honest consequence recorded: apply→plan suites largely do not split, and the selection speedup claim is reduced accordingly |
| R2-5 | CRIT | The C3 repair leaks: `count = 0` with an *indexed* dependent validates but errors at evaluation ("Invalid index") → `KilledByError` → a zero-assertion suite reports the resource as **not** pseudo-tested; `for_each` resources reject added `count` outright; and a residual line in the product design still states the C3 condition inverted | **Accepted.** Deletion is multiplicity- and use-site-aware: existing `count` → `0` only when all consumers tolerate empty collections; `for_each` → empty collection (never an added `count`); exact-index consumers get `EXT-BODY-BLANK`. Pseudo-tested status is defined over **assertion kills only**. The contradictory line is fixed |
| R2-6 | MAJ | The post-hoc `Invalid`/`KilledByError` split from test diagnostics is impossible: static and dynamic `cidrsubnet` failures emit byte-identical summary/detail | **Accepted.** Replaced with **lazy validation**: `validate` runs only after a phase-one `error`, preserving the M11 speed win for the passing/killed majority while keeping the only verified discriminator for the erroring minority |
| R2-7 | MAJ | Per-sandbox `modules.json` + providers-only sharing omits **remote module payloads** under `.terraform/modules/` — any root consuming a registry/git child fails every mutant ("Module not installed") | **Accepted.** Remote module payloads are shared read-only (they are immutable) and synthesised `modules.json` entries are rewritten to sandbox-visible paths. Excluded from mutation *generation*, present for *execution* |
| R2-8 | MAJ | Mutant states are overlapping predicates (one mutant can fail one run and error another; `Survived` and `MockMasked` overlap by definition) but the formulas require a partition — counts depend on undocumented precedence | **Accepted with modification.** Per-run outcomes are recorded orthogonally; the aggregate state is assigned by explicit precedence (`Invalid` → `Killed` → `KilledByError` → `Timeout` → fingerprint states), and `MockMasked` is demoted from a state to a **diagnosis** of `Survived`, which removes its overlap entirely |
| R2-9 | MAJ | The volatile mask is too coarse (masking a whole scalar erases the stable, assertable suffix of `"${uuid()}-stable"` — verified killable via `endswith`) and the static list is wrong (`uuidv5` is deterministic) | **Accepted.** `uuidv5` removed from the impure list. Where the AST shows a volatile subcomponent inside a template, the stable components remain in the fingerprint (provenance is knowable statically); where it cannot be soundly decomposed, the fingerprint is `indeterminate`, never treated as identical |
| R2-10 | MAJ | Mocked `apply` executes **provisioners** — `local-exec` verified running arbitrary shell with no real provider present, so the `--allow-real-infrastructure` gate never triggers | **Accepted.** Apply-mode runs containing provisioners, `external`/`http` data sources, or `terraform_remote_state` are refused without a separate explicit opt-in (`--allow-unsandboxed-effects`). Characterisation may not claim to "proceed safely" by excluding their addresses from pinning; agent-integration A1 amended |
| R2-11 | MAJ | Excluding `Timeout` from the scored set still lets machine load raise the score (K=1,S=1 → 50%; S becomes T → 100%); a `--min-score` gate can pass because survivors timed out | **Accepted with modification.** Timeouts stay in the **denominator** (score can only drop under load — conservative) and any timeout marks the score *incomplete*, which fails `--min-score` gates unless `--allow-incomplete-score` is set |
| R2-12 | MAJ | M1 promises the pseudo-tested headline but its two deletion operators require provider-schema optionality, which was scheduled M2 | **Accepted.** `providers schema -json` integration moves to M1 — it is cheap and now load-bearing there |
| R2-13 | MAJ | The only sound selection lands M3 while M2 claims the viability speedup; the §3 "post-MVP" reference-graph paragraph still contradicts the roadmap | **Accepted with modification.** Sound instantiation-reachability selection (which R2-1 makes the required basis anyway, and which is computable without the full graph) lands in **M2** with file splitting; the graph-based cone *refinement* stays M3; explanatory uses stay post-MVP. The M2 exit gate becomes the demonstrated real-provider inner loop; M1's gate is correctness plus re-measurement. The stale paragraph is rewritten |

## Pattern note

Round one's lesson was "measure against a realistic provider schema". Round two's lesson is
sharper: **three of the five criticals were introduced by round-one repairs** (the
assertion-cone exclusion by C2's fix, the leaking deletion gate by C3's fix, the splitting
semantics by M7's adoption). Repairs are design changes and get the same adversarial
treatment as the original — which is exactly why this round was scoped to the revisions.
The standing rule from round one is extended: a repair to a reviewed finding is unreviewed
design until it has itself survived review or reproduction.
