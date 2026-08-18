# M4 implementation review

What implementing M4 (issue #58, revision 2; tickets #59–#65) measured, decided and
deferred. The gates and the published measurements are in
`docs/research/11-m4-exit-gate.md`; this document is the decision record the next milestone
spec absorbs first.

## What was measured, and what the measurements corrected

1. **Terraform refuses a constant assert condition.** The suggestion-soundness gate's
   vacuous seed was specified as an "always-true assertion"; Terraform v1.15.8 rejects
   `condition = true` with "The condition expression must refer to at least one object from
   elsewhere in the configuration". The seed is therefore the tautology `<ref> == <ref>` —
   still vacuous, still baseline-green, still caught only by the isolated mutant leg. Rule 3
   (never trust a plausible reading — run it) earned its keep again.

2. **Sensitivity marks do not survive a provider hop.** The sensitivity predicate was
   specified over the payload's own mirrors (`after_sensitive`, `sensitive_values`, an
   output's flag) and their ancestors. Measured against v1.15.8: `terraform_data`'s
   computed `output` carries the value of its sensitive `input` with **no mark of its
   own** — Terraform propagates sensitivity through its expressions, not through a
   provider's computed echo. The path predicate alone would have inlined the secret through
   the unmarked twin. The shipped predicate therefore has a second layer: any string
   rendering the baseline payload marks sensitive anywhere is refused wherever it appears
   (`Payload.SensitiveRenderings`), and the report itself withholds sensitive renderings
   from delta evidence (`(sensitive value withheld)`), so no reporter can carry what the
   suggestion refused to. This is deliberately stricter than Terraform's own renderer.

3. **The cache measurement rejected the per-file key.** Full protocol and numbers in the
   exit-gate document: 3 false reuses out of 8 claimed on the seeded cross-file dependency.
   Recommendation of record for M5: no per-file key; any candidate must be
   graph-dependency-aware and must re-run the pinned protocol first. No finer key exists in
   the tree.

4. **The addressed-collection cut was bracket-blind.** `fingerprint.Address` cut instance
   addresses at the first `]`, so `resource_changes[app[0]]` yielded a nonsense address.
   Latent since M2 — nothing before the address adapter consumed the result of an indexed
   instance precisely enough to notice. Repaired with a depth-aware cut (`cutBracketed`),
   proved by the adapter matrix's `count` and string-keyed `for_each` fixtures.

## Decisions of record

- **The floor tests keep the floor honest after the slice.** M4.0's cases run under a
  `DisableJSONReading` seam control (a deliberate parallel to `DisableStaticShortcuts`), so
  the same fixtures prove both halves: refusal decided from unreadness, and lift decided
  from content. Without the control, landing M4c would have silently converted the entry
  gate's cases into slice cases.
- **JSON reads are all-or-nothing per file.** A `.tf.json` or `.tftest.json` decodes into a
  scratch inventory and merges only after complete success; a file that fails half-way
  contributes nothing. Well-formed content outside the reader's schema
  (`ErrUnmodelledJSON`) keeps that file's floor down — modelled-versus-parseable is the
  distinction that keeps a future Terraform construct from becoming silent permission.
- **JSON-derived closure references are imprecise by construction.**
  `hcl.Expression.Variables` reports which addresses an expression observes and nothing
  about how, so a JSON-mediated read degrades to `unasserted` rather than claiming a
  weak assertion the reader cannot prove.
- **One suggestion per survivor.** The generator emits the first change all three adapters
  admit, or the first refusal's own status. Several assertions per survivor is unmeasured
  value at real verification cost (each suggestion is one full-suite run plus one isolated
  run); the outcome model does not preclude widening later.
- **`--apply` re-verifies in the same invocation.** Selection by ID or `--all-verified`
  operates on the current report's suggestions, freshly verified this run, so the digest
  binding guards the concurrent-editor race rather than a workflow gap; the preflight's
  digest row is proved at its own seam (`apply_internal_test.go`) because no engine-seam
  test can stage that race deterministically.
- **The vacuous-seed correction and the sensitivity second layer** (above) are repairs to
  reviewed findings and are therefore flagged here as candidates for the next adversarial
  review, per standing rule 2.

## The testing seam

One exception added, recorded in `AGENTS.md` alongside the existing three:
`internal/suggest`'s adapter matrices are exercised directly over payload paths and
published provider types (fifteen shapes across addressing and rendering), for the same
reason `internal/fingerprint` is — they are contracts about documents, and the real binary
cannot be driven into each shape on demand. Every adapter outcome is still asserted through
the engine seam in `suggest_test.go`.

## Deferred, and to whom

- **M4.5**: `StructurallyUnassertable` skeleton generation, behind the minable-validation-
  share measurement (C4 relocation, recorded in `characterisation.md`'s sequencing); the
  characterisation skill (`tf-mut-characterise`) joins `skill install` there.
- **M5**: any finer cache key, gated on the rejection rule above; the multi-suggestion-
  per-survivor question, if survivor density ever makes it worth a measurement.
- **Open question**: the suggestion engine renders element assertions only where the
  provider schema types the collection; `terraform_data`'s `dynamic`-typed attributes
  therefore always skip. A typed-fixture provider (the mirror already carries
  `hashicorp/null`) would widen the rendering matrix's positive rows from schema evidence
  rather than from fabricated schemas. Worth a spike before M4.5 leans on rendering.

## Addendum: the PR #69 adversarial review (2026-08-18)

The delivery review found five contract breaches, all confirmed and repaired in
the same change, each now a named gate case:

1. **The suggest surface was unreachable.** `parse` never mapped the five
   suggest fields into `engine.Config` — a silent no-op in the wiring edit that
   built it, which the whole engine-seam suite could not see because the seam
   starts below the CLI. Wired, and covered by three command-level tests
   (dry-run, survivor selection, apply selection). Lesson recorded: a public
   shell needs at least one command-level test per command, because the seam's
   thoroughness proves nothing about the shell.
2. **JSON-declared module calls were outside the closure.** The generic-
   reference path never created a `ModuleCall`, so a JSON-called child's
   providers and effects bypassed both gates — the R2-10 shape again, one level
   up. JSON calls now enter `module.Calls` (inputs undecoded, so the graph
   marks the call unbounded and generation skips it), reproduced through the
   engine seam with an unmocked-provider child and a provisioner child.
3. **Two readers accepted content they did not model.** The `terraform` block
   discarded its remainder; a JSON run accepted every attribute while reading
   only `command`. Both now refuse unmodelled content (`required_version` is
   the one recorded allow-list entry), and `expect_failures` and the override
   blocks were removed from the run schema so they refuse rather than decode
   to nothing.
4. **The reported patch was not the verified bytes.** Candidate patches were
   rendered with a different message than verification and apply used. One
   renderer now produces all three, and a gate case asserts every added patch
   line appears verbatim in the applied file.
5. **The apply protocol had a check-then-replace race.** The preflight's digest
   and file identity now travel to the commit step, which re-resolves the path,
   re-checks device/inode and digest immediately before the rename, and aborts
   on any change; a probe seam stages the race deterministically. The residual
   window is the instants between the final read and the rename — the
   narrowest a content-conditional replacement can be without a filesystem
   compare-and-swap, and recorded here as such.

## Addendum: the round-3 adversarial review (2026-08-18)

Twelve findings; ten repaired, one repaired at its root (the cache), one
declined with reasoning. Each repair is a named gate-m4 case.

1. **(Critical) A mutant-surfaced secret was published.** Sensitivity was
   decided over the baseline payload alone, so a collection mutant that moved
   a secret into a path whose baseline value was public printed it in the
   terminal and JSON reports. The predicate now reads both payloads' mirrors
   and unions the secret renderings across every run and both sides; the
   `sensitive-shift` fixture reproduces the leak (proven red before the fix)
   and every reporter is asserted value-free.
2. **(Critical) `check`/`moved`/`import`/`removed` in the JSON schema lifted
   the floor while nothing collected them.** A check-scoped data source
   reached neither inventory. The four left the schema, so their presence now
   leaves the file unread. The HCL reader has the same blind spot — a
   pre-existing gap outside this milestone's diff — filed as its own issue.
3. **(Critical) A JSON `mock_provider` body was decoded for `alias` alone.**
   `override_during`, `source`, `mock_resource`, `mock_data` decide what a
   mock covers; anything beyond `alias` now leaves the file unread.
4. **(Major) Flags after the module path were silently discarded** — Go's
   flag parsing stops at the first non-flag argument, so `suggest . --dry-run`
   verified anyway. Trailing arguments are refused by name.
5. **(Major) One expression killing N mutants produced N identical assert
   blocks.** Candidates sharing (file, run, expression) now collapse into one
   suggestion carrying the rest in `also_kills` (2.2.0, additive); the
   isolated leg still runs once per claimed mutant, so attribution stays
   per-mutant, and apply writes the assertion once.
6. **(Major) The published schema carried none of the outcome table's
   presence rules.** `report-2.2.0.json` now encodes them (`allOf`/`if`/
   `then`/`not`), both suite validators grew the vocabulary, and a real
   engine-produced suggest report — one with verified rows, one all skips —
   is validated against the published document.
7. **(Minor) `--dry-run --all-verified` reported applying nothing** — the
   combination is refused before any work, as is a test selection with
   suggest (verification runs the full suite by contract, and a filtered
   population could launder an excluded run's kill).
8. **(Minor, declined) File-level `variables` in `.tftest.json` decode and
   are not merged.** Declined with reasoning, recorded at the schema: they
   scope to this file's runs only, every such run is `JSONDeclared`, and the
   evaluator fails closed on those before any variable is read — nothing that
   could act on the dropped content exists, and the class informs neither
   gate.
9. **(Minor) The non-identifier-map-key gate row proved nothing** — its
   fixture used two ordinary identifiers. The row now uses a key with a
   space, and the adapter routes an attribute-path parse failure to
   `skipped-unrenderable` (C3's status) when the resource address alone is
   legal.
10. **(Minor) One exit-gate sentence misattributed a measured number** — the
    coarse key discards all 11 verdicts on a comment edit, not 8. Corrected.
11. **(Minor, latent) Verification ignored `TestSelection`** — unreachable
    from the CLI, now refused at the seam (see 7).
12. **(Minor) `.tfmock.hcl`/`.tfmock.json` were outside the cache key** — a
    verdict-affecting file class nothing hashed, pre-existing since M3. The
    key now hashes every mock-data file under the closure
    (`TestMockDataFilesAreAKeyDimension`). Their absence from the floor is a
    recorded non-issue: mock data can declare no provider, effect or run, so
    it informs neither gate — the same disposition as the variables class —
    and the class is noted in the M4.5 handover.

## Briefing for the next milestone's spec author

What M4's implementation and its three adversarial rounds taught, distilled to what
changes how the next spec should be written. Everything here is earned in this
milestone's history; nothing is aspirational.

1. **Specify the shell, not only the seam.** M4's worst self-inflicted defect was a
   `suggest` command whose five flags never reached the engine — invisible to the entire
   engine-seam suite because the seam starts below the CLI, and invisible to the author
   because the wiring edit failed silently. Every spec that adds a public command or flag
   should require at least one command-level acceptance case per command *and per argument
   order* (round 3 found that flags after the module path were silently discarded, so the
   one wiring test that existed proved only one ordering).

2. **"Enumerated in a schema" is not "modelled"; make specs demand the negative case.**
   Three separate fail-opens had the same shape: a construct the reader *accepted* but did
   not *collect* (JSON `check` blocks, `mock_provider` bodies, `terraform`-block
   remainders, run attributes). The M4 spec's own checklist line — every emission into a
   foreign grammar needs its own fail-closed adapter — was necessary but not sufficient;
   the sufficient form is: **for every construct a reader's schema names, the spec must
   say what is collected from it, and everything else must have a refusal fixture.** A
   useful spec-review question: "list the schema entries; which acceptance case proves
   each one's remainder is refused?"

3. **Safety predicates must be specified over every payload that exists, not the obvious
   one.** The sensitivity predicate was specified over "the exact baseline path and its
   ancestors" — and a mutation moved a secret into a path whose *baseline* value was
   public, so only the mutant payload carried the mark. When a spec defines a predicate
   over "the payload", it should name which payloads exist at that point (baseline,
   mutant, re-run) and require the predicate over all of them, with a fixture whose
   interesting fact lives only in the non-obvious one. Corollary from the same finding:
   marks that do not propagate (Terraform drops sensitivity across a provider's computed
   echo) mean value-based guards are needed alongside path-based ones — specify both.

4. **Check-then-act protocols need their re-check specified at the act.** The apply
   protocol's C6 repair specified the preflight; the race between preflight and rename was
   only closed in round 3. When a spec orders "verify, then write", it should state where
   the verified identity travels to and what re-proves it at the final step — and require
   a deterministic seam (a probe hook) for the race no black-box test can stage. The same
   lesson generalises: our seam controls (`DisableStaticShortcuts`, `DisableJSONReading`,
   `SeedSuggestionDefect`, `applyCommitProbe`) were each the only way to keep an
   already-repaired behaviour provable after the repair; specs should name the seam
   control alongside the behaviour.

5. **Identity keys deserve a design sentence.** Suggestion identity included the mutant
   ID, so one assertion killing five mutants became five suggestions, five verification
   runs and five identical assert blocks. When a spec gives an entity a stable ID, it
   should also say what the ID *deduplicates over* — and what the user-visible cardinality
   is meant to be.

6. **Published schemas must encode their presence rules, and be validated against engine
   output.** The 2.2.0 outcome table's rules ("every skipped status carries no patch")
   lived in Go tests and description prose until round 3; a consumer validating against
   the published contract got none of them. Future schema changes: conditionals in the
   schema, plus one suite case that validates a *real* report per interesting shape.

7. **Platform facts still bite last.** Two more measured this milestone, both after the
   spec was "done": Terraform refuses a constant assert `condition` (the soundness gate's
   vacuous seed had to become a tautology), and `GITHUB_STEP_SUMMARY` is per-step (the
   workflow test had to assert the summary's on-disk source instead). Rule 3 — run it,
   don't reason about it — applies to the CI platform as much as to Terraform, and specs
   should ask for the platform-fact measurement in the same slice as the feature, not
   after it.

8. **Formatter parity is part of "done".** The only red CI round after three green local
   gates was gofumpt-vs-gofmt drift: local verification used the weaker formatter. Specs
   need not say this, but the delivery checklist should: run the repo's own recipes
   (`just fmt-check`, both lint tag-sets) rather than their approximations.

9. **What the next specs inherit, concretely.** M4.5: the relocated skeleton work behind
   the minable-share measurement, the characterisation skill, and the open question of a
   typed-fixture provider so the rendering matrix's positive rows come from real schema
   evidence. M5: any finer cache key must be graph-dependency-aware and re-run the pinned
   protocol (the per-file key is rejected on 3 false reuses); mock-data files
   (`.tfmock.*`) are hashed into the cache key as of this PR but are in no floor class —
   decide their class if Terraform ever lets a mock body carry more than values. And #70:
   the HCL `check`-block inventory blind spot, reproduced end-to-end, waiting on a real
   collector for both syntaxes.
