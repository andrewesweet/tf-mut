# M4.5 implementation review — 18 August 2026

What implementing issue #71 decided, what it measured, and where it landed narrower than the
spec's words. The measurements themselves are in `docs/research/13-m45-exit-gate.md` and
`docs/research/12-m45-synthesis-rate.md`; this document is the decision record.

## Decisions taken during implementation

**The staged suite is a materialised overlay, not an in-memory one.** The M2 disposition asks
for a suite that discovery, mutation execution, suggestion targeting and verification all
consume. Discovery reads the file system, so the overlay is materialised into a staging root —
the closure plus the generated files, written as fresh inodes — and the whole pipeline is
pointed at it. The alternative, threading an overlay through four contracts that all take a
directory, would have changed every one of them to buy nothing the staging root does not give.
`sandbox.Spec.Staged` is the seam; it writes a path whether or not the source tree declares
one, which `Mutations` deliberately does not.

**An answered judgement point is accepted where a synthesised one is refused.** Both go
through the same static evaluator over the module's own validation conditions, and the
evaluator returns a pair — *does it hold* and *could it be decided*. A value the tool
synthesised must be *proven acceptable*, so an undecidable constraint refuses it. A value the
reader supplied must be *proven unacceptable*, so an undecidable constraint lets it through to
the real plan. Getting this backwards was the first version's bug: `can(cidrnetmask(...))` is
not in the evaluator's function table, so a perfectly good `"10.0.0.0/16"` was rejected and the
answer loop could never close. The asymmetry is the design, and it matches
`agent-integration.md` §2.4: nothing an agent supplies is trusted, and what verifies it is
execution.

**The non-executable artefact class is an extension, not a marker.** `.tfmut-todo.hcl` is a
file `terraform test` never reads. That single fact is what lets three contracts hold at once —
a judgement point fails loudly, the suite on disk is green by construction, and the file an
agent edits is the file a resume reads. A placeholder inside a `.tftest.hcl` cannot do it,
which is exactly what the review's C2 refuted.

**Provider configurations, not provider requirements.** `characterise.Configurations` is the
list the staged provider gate is judged against: the default configuration of each required
provider, plus every declared alias. One mock per requirement leaves each alias reaching a real
provider, because Terraform matches mocks to configurations by alias.

**Pins go through `suggest`'s adapters unchanged.** The pinning stage builds a `report.Change`
and calls `suggest.Express`. There is no second renderer: a value that is unrenderable for a
suggested assertion is unrenderable for a pinned one, and the four skip classes the
`configured` rung reports are that contract's, not a new one.

**Redaction is decided at the variable, not at the value.** A sensitive variable withholds its
whole evidence bundle — constraint, diagnostic and every attempted value. The constraint
matters most and is the least obvious: `var.token == "…"` names the secret outright, so quoting
the constraint verbatim into a TODO would publish it. The mandatory fixture's secret exists
only in a failed synthesis attempt, which is before the point the M4 predicate started at.

## What the two-axis review repaired

The change was reviewed along both axes before it landed. Eight findings were repaired rather
than argued with; the four that mattered are named in `docs/research/13-m45-exit-gate.md` §8.
The pattern across them is worth its own line, because it is not the pattern the spec review
found:

**Every one was a contract the passing tests could not have caught, because each test asserted
the *outcome* the contract implies and not the *property* the contract is about.** The
acceptance pair asserted that a missing alias mock produces a refusal — true both before and
after the gates moved ahead of `terraform init`. The write-protocol cases asserted that a
collision is refused — true whether the digest's source leg is live or frozen. The answer loop
asserted that a good answer is promoted — true whether a bad one is rejected or aborts the
run. In each case the test was about the happy path of a safety property, and the property
itself went unasserted until something read the code rather than the results.

## Where the implementation is narrower than the spec

Recorded in full in the exit gate, §7. In short: scaffold promotion is emission-only (a TODO
can be answered and promoted, a scaffold cannot); assertion provenance is decided at file
granularity because that is what a content digest can honestly support; and the staged run's
verdict cache is disabled rather than keyed on staged bytes, which satisfies the safety
property the disposition protects and forgoes the speed it implies.

## Flagged for the next review under standing rule 2

A repair to a reviewed finding is unreviewed design until it has itself survived review. Three
arrive unreviewed:

1. **The effective-staged-suite gate evaluation** (C1's repair). The gates now judge a program
   that does not exist on disk. The refusal path is proven; what is unreviewed is whether the
   *planned* scaffold can diverge from the *executed* one in any way the acceptance pair does
   not catch.
2. **The non-executable promotion protocol** (C2's repair). Promotion is the only route into
   executable content, and it runs the same verification the write protocol runs. What is
   unreviewed is the asymmetry above — accepting an answer an undecidable constraint cannot
   clear — together with its failure path: a wrong answer is now rejected with the diagnostic
   attached and the artefact rewritten, which is the reported-finding shape
   `agent-integration.md` §2.4 asks for, and which nothing had asserted until the review.
3. **The staged-suite overlay** (M2's repair), and its consequence that the staged run's cache
   is scoped to a directory that is about to vanish.

And one measured cost, which is not a repair but is squarely a rule-2 question:

4. **The `moved` refusal costs one corpus module in ten.** `import` names a provider
   configuration and reads a real resource at plan time — the R2-10 fail-open shape. `moved` is
   state bookkeeping with no provider, no effect and no evaluation. The C4 disposition disposed
   of them as one construct, and the measurement says that costs real modules.

## Pattern note

Two lessons, and they point the same way.

The first repeats the spec review's: **the cheapest defects were the ones a measurement found,
and the measurement only found them because it ran the shipped pipeline rather than a proxy for
it.** The corpus run surfaced two facts nothing in the design predicted — mining fires never,
and a `moved` block makes a major public module uncharacterisable — and both came from driving
`tf-mut todos` through the real seam over real modules. The form census this replaced would
have reported a share and changed nothing.

The second is the review's: **a test that asserts the outcome a safety property implies does
not assert the property.** Four contracts were broken under a fully green gate, each behind a
case that checked what the contract produces rather than what it forbids — a refusal happened,
but after `init`; a collision was refused, but from a frozen digest; an answer was promoted,
but a wrong one aborted. The repair in each case was to assert the forbidden thing directly:
the invocation log, the staged closure change, the rejected status. Both lessons are the same
instruction in different clothes — **assert the thing the contract is about, not the thing you
expect to see when it holds.**
