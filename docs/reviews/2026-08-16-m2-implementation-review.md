# M2 implementation review — 16 August 2026

What implementing milestone M2 ([issue #21](https://github.com/andrewesweet/tf-mut/issues/21)
and its sub-scope tickets #22–#30) taught, recorded the way M1's review was, so that the M3
spec absorbs it rather than rediscovering it.

The same two kinds of entry, and the difference still matters:

- **Measurements** are facts. Standing process rule 1 gives them force over design prose, and
  the losing documents have been corrected in the same change.
- **Decisions** are what the implementation did about them. A later spec may revise one, but it
  should say so deliberately.

The recommendations are collected at the end as **open questions**. They carry no precedence;
the M3 spec author is expected to dispose of each, in either direction.

---

## Measurements

| # | Measurement | Consequence |
| --- | --- | --- |
| M2-A | **A buffering decoder peaks at 240 MB of live heap where the streaming one stays under 64 MB**, four workers, ≥ 100 MB stream, measured by `runtime/metrics` `/gc/heap/live:bytes`. | The M5 bound discriminates by 3.7×, so it fails a buffering decoder by construction rather than by tuning. Published with method in `../research/08-m2-exit-gate.md`. |
| M2-B | **`terraform test -verbose -json` serialises `depends_on` into `test_state`.** A `DEPENDS-DROP` mutant therefore moves the payload, while remaining unassertable by construction: `terraform test` assertions evaluate HCL against the module, and no expression reads a resource's dependency edges. | The fingerprint drops the payload members an assertion cannot reach — `provider_name`, `schema_version`, `mode`, `depends_on`, `sensitive_values`, `replace_paths` and their neighbours. Without this, every `DEPENDS-DROP` mutant would be reported as an ordinary survivor, which is exactly the crying-wolf the `StructurallyUnassertable` state exists to prevent. Standing process rule 3, again: nobody would have predicted this from the design. |
| M2-C | **Two observations of a value cannot establish component granularity.** Two random hexadecimal identifiers share a leading character about six times in a hundred; a span inferred from the characters two baseline runs happen to have in common calls a volatile character stable, and every later run that disagrees is undecidable. | Run-derived masks are whole-value. Component granularity comes from the syntax, where the impure subcomponent is visible rather than inferred — which is what the R2-9 disposition actually says. The alternative is a verdict that changes with the weather. |
| M2-D | **At Tiers 1–3, `Invalid` is 0.5% of the population and `KilledByError` is 7.7%** (222 mutants, 66 operators, applicability-matrix fixture). | Answers M1 open question 3. Lazy validation runs `validate` for 8.2% of mutants against 100% for eager; selective validation by operator would save a fraction of that and add a policy to maintain. Recommendation: keep lazy validation, do not build selective validation. |
| M2-E | **Two operators were 100% `Invalid` and the per-operator counts found both.** `CHECK-REMOVE` removed a check's only assertion, which Terraform rejects; `OUT-SENSITIVE-FLIP` cleared `sensitive` on an output reading a sensitive variable, which Terraform also refuses. | Both repaired by adding the evidence gate the matrix row now records. The population's `Invalid` share fell from 1.3% to 0.5%. This is the argument for publishing per-operator counts rather than a single error rate: an aggregate of 1.3% looks like background noise, and the split showed two operators that never produced a usable mutant at all. |
| M2-G | **`issensitive` is a Terraform function and an assertion over it passes.** The fingerprint's exclusion list was drafted to drop `sensitive_values`, `before_sensitive` and `after_sensitive` alongside the other bookkeeping members, on the reasoning that sensitivity is metadata. | Running it refuted that in one test. The three members stayed in the fingerprint; dropping them would have made every `OUT-SENSITIVE-FLIP` and `VAR-SENSITIVE-FLIP` delta invisible to the oracle. The list was drafted by reasoning and corrected by measurement, which is standing process rule 3 catching the same class of error a third time in one milestone. |
| M2-F | **222 mutants over a fourteen-block module run two-phase in 4.4 s offline**; the whole offline suite, including three repeated-run determinism cases, is 21 s. | The two-phase split's cost is invisible on small-schema providers, as M1 predicted. It buys the fingerprint for the non-killed minority only. |

## Decisions

| # | Finding | Disposition |
| --- | --- | --- |
| M2-1 | The normative diagnosis precedence lists `indeterminate-unknown-values` first, and a plan-mode mocked payload almost always carries unknowns. Read literally over every survivor, that diagnosis would swallow the entire plan-mode population and the rest of the engine would be dead code. | **The unknown rule gates equality claims, not difference claims.** A survivor with a proven masked delta is diagnosed from the delta; the indeterminacy diagnoses apply where the oracle would otherwise have to claim identity. The predicate's own wording carries this — "and fingerprint equality therefore unprovable" — but it is worth stating, because the other reading is available and would be silently useless. |
| M2-2 | The C4 rule says a delta that empties after the mutant re-run "follows the fingerprint-identical rules". Applied literally to a value that is stable in the baseline and volatile only under the mutant, that produces `Unobservable` — a false proof, for a mutant an ordinary equality assertion would kill. | **A path volatile in the mutant and stable in the baseline is undecidable, not maskable.** Masking it on both sides erases the difference the mutation made. Paths the baseline already knew were volatile keep their spans; this is the "residual undecidability" the rule's last clause names, and it is why the C4 fixture classifies `indeterminate-volatility` rather than `Unobservable`. |
| M2-3 | `Delta.Indeterminate` initially poisoned the whole comparison, so one undecidable path anywhere turned a payload full of proven changes into an indeterminate verdict. | **A proven change survives an undecidable neighbour.** The change was observed over components the mask left alone; a mutant that moved something the suite could have asserted on is a finding whatever else about the payload could not be compared. Indeterminacy decides only where there is nothing else to go on. |
| M2-4 | `resource_changes` and the state's resource lists are arrays, and the normative fingerprint contract says arrays are canonicalised in payload order. | **Arrays whose elements each carry an `address` are keyed by it.** Order-independence there is a correctness requirement rather than a convenience: letting an inserted resource shift every index would turn one mutation into a whole-payload delta and break every diagnosis that reads the delta's addresses. Generic arrays keep payload order, as specified. |
| M2-5 | `provider = null.primary` is the ordinary way to place a resource on an aliased configuration of the null provider, and HCL parses `null` as the null keyword — so the expression is a traversal relative to a literal, not the reference it reads as. | **`PROVIDER-ALIAS-SWAP` reads its reference from the source text**, as Terraform's own decoder effectively does. Discovered by the alias fixture generating nothing. |
| M2-6 | The M1 identifier derivation — operator, path, site address — collides once Tier 1 operators fire many times inside one attribute. | **Site content and replacement joined the derivation**, per the M4 disposition's own wording. Identifiers still survive a line move and an unrelated edit, and are still broken by renaming the file, which the schema documents. Schema 2.0.0 was already a break, so nothing was owed to a consumer here. |
| M2-7 | The streaming memory bound is a claim about this project's retained heap, and the fixed seam is the engine entry point against the real Terraform binary. | **Measured at the decoder, in `internal/tfexec`, with a `//nolint:testpackage` and a stated reason.** A 100 MB real stream cannot be produced inside a millisecond-fast offline suite, and there is no Terraform to fake: the input is recorded real output with its provider schemas inflated. A deliberate, narrow deviation from the seam, recorded here rather than quietly taken. |
| M2-8 | The `operators` fixture must carry a generation site for every enabled operator, and `DYNAMIC-ZERO` needs a provider whose schema declares a nested block type. | **Split into a preview-only fixture.** `dynamic` backs the operator's generation site and mutation text; its end-to-end classification waits for a real provider. Keeping it in the main fixture would have reddened that fixture's baseline and cost the other sixty-five operators their end-to-end coverage. |
| M2-9 | Emptying a heredoc's trailing newline segment produces a document that does not parse. | **The generator re-parses every mutant and discards the ones that do not, with a warning naming the operator** — that is an operator defect, not a finding about the module, and spending a Terraform run to discover it would be waste. Whitespace-only template segments are now skipped outright. |
| M2-10 | `EXT-RESOURCE-DELETE` and `COUNT-ZERO` fire under identical conditions and emit identical text. | **Deduplication is by mutated file content, and the Tier 0 entry wins.** Recorded in the matrix rows rather than left for a reader to wonder where the Tier 2 mutants went. |

---

## What review found after the milestone was declared done

Recorded because the pattern matters more than the individual defects: all four were in code
the gate ran green over, and three were invisible to any test the milestone had written.

| # | Finding | Disposition |
| --- | --- | --- |
| M2-11 | The closure's child walk used `hclsyntax.VisitAll`, which walks the whole subtree rather than the direct children. A splat nested inside a function call was therefore reached twice — once as itself, marked imprecise, and once as an ordinary descendant, marked precise — so the second visit handed back a precise reference for a projection nobody can follow. | **Direct children are enumerated by hand.** The honest `unasserted` fallback would otherwise never fire where it matters most: on a projection wrapped in a call, which is the shape a real module writes. Pinned by `TestASplatInsideACallStillDefeatsTheClosure`. |
| M2-12 | `StructurallyUnassertable` was ordered below the unknown rule, and a plan-mode payload almost always carries unknowns. | **The state comes first.** It claims nothing about equality — the construct has no projection at all, which is true whatever the payload contains — so the conservative unknown rule has nothing to protect there. As shipped, every untested contract in a plan-mode suite would have been reported as `indeterminate-unknown-values`, which is story 4 failing silently. The `contract` fixture was apply-only, so nothing caught it. |
| M2-13 | `mock-masked` was gated on the *suite* having an apply run rather than on the delta's own runs, so a plan-mode change in a mixed suite could be blamed on a mock. And `Derive` skipped a run block one baseline run fingerprinted and the other did not, so a later comparison could call that run identical. | **Both narrowed.** Apply mode is a property of the runs the delta came from; a run present on one side only poisons the comparison rather than vanishing from it. |
| M2-14 | A configured path exclusion fell back to a prefix match, so `generated` silently excluded `generated-other.tf` — and an over-broad exclusion raises a mutation score without anybody noticing. | **The fallback is gone.** Configuration must never fail in the direction that flatters the score. |

Two smaller ones worth the line: the SARIF document reported an exit code computed from an
empty gate, so it published a code the run never returned — it now reports none; and
`indeterminate-volatility` could reach the report with an empty evidence field where the
mutant had not been re-run, which is a diagnosis the reader cannot act on.

## Open questions for the M3 spec

Not dispositions. Each needs an answer, and "no" is a legitimate one.

1. **Does `mock-masked` survive contact with a real provider?** It has no offline reproduction:
   the diagnosis fires on an apply-mode delta confined to schema-`computed` attributes, and
   neither `terraform_data` nor `null_resource` has an attribute that is both configurable and
   computed, so no mutation can move a computed attribute without also moving a configured one.
   The false case is proven; the true case is not. M3 brings a real-provider fixture for its
   inner-loop gate, and that fixture should carry this reproduction — or the diagnosis should
   be withdrawn. Shipping a diagnosis whose positive case has never fired is exactly the kind
   of thing this milestone exists to refuse, and it is stated here rather than left implicit.
2. **Should `Unobservable` retain a length class?** A wholly-masked volatile value compares
   equal to any other value of any length, so a mutation that deletes the volatile component
   entirely — `"${uuid()}-stable"` to `"-stable"` — is reported unobservable. An assertion on
   the value's length would catch it. Encoding length in the mask marker would recover that,
   and would flake on any volatile value whose length varies, which `random_integer` produces.
   The conservative choice was made; the trade is worth re-examining with the attribute-level
   graph.
3. **How should the closure treat a whole-object read?** An assertion reading `output.x` where
   `output.x = terraform_data.app` matches every delta under that resource and diagnoses
   `weak-assertion`. That is defensible — the object genuinely is read — but it is the coarsest
   answer the closure gives, and M3's attribute-level graph could say which attribute.
4. **Is the module-level `NoCoverage` claim still worth having?** It fires only where no
   selected run instantiates the module at all, which the cost model measured as 0% of a
   single-module suite. M3's conditional-instantiation analysis is what makes the state
   informative; until then it is nearly always empty, and the spec should decide whether an
   always-empty state earns its place in the scored set.
5. **Should the terminal reporter bound its delta differently?** It prints three changes per
   survivor and the JSON carries twenty. Both numbers were chosen rather than measured, and a
   real module's survivor list is the first place a user forms an opinion of the tool.

---

## Process note

The milestone's most expensive corrections were all of one kind: a rule that reads
unambiguously in the spec, and has a second reading that only running the code reveals.
M2-1, M2-2 and M2-3 are each a case where the literal reading produced a false proof or a dead
engine, and each was caught by writing the fixture the spec asked for and finding the verdict
absurd. **The reproductions are not a check on the implementation; they are how the
specification gets finished.** Round one's lesson was measure against a realistic provider,
round two's that repairs need the same scrutiny as originals, M1's that the scheduled gates are
load-bearing. This one's is that **a normative table is a hypothesis until a fixture has
disagreed with it**.

The second note is smaller and worth keeping: M2-B and M2-E were both found by machinery the
milestone built for another purpose. The fingerprint's own evidence showed `depends_on` in the
state payload; the per-operator error counts, built to answer someone else's open question,
showed two operators that had never produced a usable mutant. Instrumentation earns its keep in
places nobody aimed it.
