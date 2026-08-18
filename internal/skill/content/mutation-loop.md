---
name: tf-mut-mutation
description: >-
  Drive the tf-mut mutation-testing loop over a Terraform module: run the
  grading, read the three metrics together, act on each survivor through its
  diagnosis, and let `tf-mut suggest` generate and verify the fixing
  assertions. Use whenever the task is to raise a module's mutation score or
  to act on a tf-mut report.
---

# The tf-mut mutation loop

tf-mut grades a `terraform test` suite by mutating the module and checking
whether the suite notices. Your job in this loop is judgement — choosing what
each finding means for this module — and the tool's job is ground truth. Let
it verify every step.

**This skill is explicitly forbidden from having the agent hand-write
assertions the harvest can generate, because harvested assertions are
evidence and hand-written ones are guesses.** That rule binds you. When a survivor is fixable by an assertion, drive `tf-mut suggest`
and apply what it verified; write an assertion by hand only when `suggest`
reported it cannot express one, and say so.

## The loop

1. **Grade.** `tf-mut run --reporter json <module>` (add
   `--since <ref>` in a PR workflow to scope the population to what changed).
   Read the JSON, never the prose.
2. **Read the three metrics together.**
   - *Mutation score* — killed ÷ scored. The headline, but never alone.
   - *Assertion score* — kills by assertions only. A high mutation score with
     a low assertion score means Terraform's own errors are doing the
     catching, not the tests.
   - *Reachability* — how much of the population the tests execute at all.
     Low reachability means missing run blocks, not weak assertions.
3. **Walk the survivors by diagnosis** (the decision tree below). Every
   survivor carries exactly one diagnosis and its evidence.
4. **Generate the fixes.** `tf-mut suggest <module>` verifies every
   suggestion by execution: the full suite stays green with it applied, and
   the mutant dies against it alone. `--dry-run` previews the patches;
   `--survivor ID` scopes to the survivors you are working;
   `--apply ID` or `--all-verified` writes the verified ones.
5. **Re-grade** and repeat until the survivors that remain are ones you have
   deliberately accepted.

## The survivor-diagnosis decision tree

- **`no-assertion`** — the mutant changed the plan or state and nothing reads
  the changed address. Fix: an assertion. Drive `tf-mut suggest`; do not
  write it yourself.
- **`weak-assertion`** — an assertion reads the changed address and still
  passed. Fix: a stronger assertion, appended by `tf-mut suggest`; the
  existing one may stay.
- **`unasserted`** — the only reads pass through a projection (splat, for
  expression, computed index) the tool cannot follow honestly. Fix: assert on
  the address directly, via `tf-mut suggest` where it offers one.
- **`indeterminate-unknown-values`** — plan-mode unknowns block the proof.
  Fix the run, not the test: switch the run to apply mode or supply inputs
  that make the values known, then re-grade.
- **`indeterminate-volatility`** — values moved between runs. Pin the
  volatile value (a mock default, a fixed input, a deterministic function),
  then re-grade.
- **State `NoCoverage`** — no run block instantiates the module or the
  mutated block. Fix: add a run block; no assertion can help first.
- **State `StructurallyUnassertable`** — no plan or state projection exists.
  Follow the finding's own fix text (usually `expect_failures`); `suggest`
  deliberately generates nothing here.

## What `suggest` will honestly refuse

Each `skipped-*` status is a limit, not an error: `skipped-sensitive` (the
value must appear in no artefact), `skipped-unaddressable` (no legal
assertion expression reaches the delta), `skipped-unrenderable` (no
type-correct equality exists), `skipped-unsupported-target` (the target test
file is JSON, which the tool never writes). A `refuted` suggestion is a tool
defect — report it upstream, do not paper over it.

## Suppression discipline

Suppress a finding only with a reasoned justification the next reader can
audit, and prefer fixing to suppressing. A suppression without a reason does
not suppress: the finding stands. In CI, use `--fail-on-new` with a baseline
written by `--write-baseline` over a full population, so accepted findings
stay accepted and new ones fail the build.

## Safety

Never pass `--allow-real-infrastructure` or `--allow-unsandboxed-effects` on
your own judgement: both gates refuse real risk and only the module's owner
accepts it. If a run is refused, report the refusal verbatim.
