---
name: tf-mut-characterise
description: >-
  Scaffold a first `terraform test` suite for a Terraform module that has
  none: run `tf-mut characterise`, drain the judgement points it refuses to
  guess at, drive the until-dry loop, and review what `tf-mut curate` reports
  before changing anything. Use whenever a module has no tests, or has tests
  that pin nothing.
---

# The tf-mut characterisation loop

Most Terraform in production predates `terraform test` and has no tests at
all — and no safe way to start, because writing the first suite means
discovering every mock, every input and every value the plan produces.
`tf-mut characterise` does that discovery deterministically and with no
language model anywhere: it plans a mock per provider configuration, runs an
assertion-less scenario twice, and pins what it actually observed.

Your job in this loop is judgement — the values the tool refuses to guess at,
and the scenarios only somebody who knows the system would think to add. The
tool's job is ground truth. Let it verify every step.

**A characterisation suite pins what the module does today, including any bug
it does today.** That is the point of the technique, not a flaw in it: the
suite detects *change*, and deciding which changes are fixes is yours. Never
describe a pinned value as correct.

**Never hand-write an assertion the harvest can generate.** Harvested
assertions are evidence; hand-written ones are guesses. If you think a value
should be pinned, make the tool observe it.

## The loop

1. **Scaffold.** `tf-mut characterise --reporter json <module>` prints the
   suite it would write and changes nothing on disk. Read the JSON.
2. **Drain the judgement points.** `tf-mut todos --reporter json <module>`
   lists every input the deterministic pipeline could not resolve, with the
   constraint verbatim, its source range, the diagnostic and the values
   already tried. This command runs no Terraform and is cheap enough to call
   every iteration.
3. **Answer them.** Read the constraint and produce a value that conforms.
   Either edit the `TFMUT_TODO` placeholder in the non-executable artefact and
   run `tf-mut characterise --resume <module>`, or pass
   `--answer todo-<id>=<value>` for a scripted run. The tool re-plans your
   answer and promotes it only once the suite it produces is green. Do not
   write yet: the suite is written once, at step 5, and a second write of the
   same target is refused as a collision.
4. **Close the gap.** `tf-mut characterise --until-dry --reporter json
   <module>` grades the scaffold and pins whatever its survivors still yield,
   at the granularity you chose and no higher. Read the convergence evidence:
   rounds, new pins per round, stop reason.
5. **Write it.** `tf-mut characterise --write <module>` places the verified
   suite — adding `--resume` if you answered a judgement point by editing the
   artefact, so the answer travels with it. The suite is proven green in a
   sandbox before a byte is written, and `--write` is the only step that
   writes: everything before it changes nothing on disk.
6. **Review the redundancy.** `tf-mut curate --reporter json <module>` reports
   assertions whose evidence says they sense nothing new. Read every finding
   against intent before acting on it; curate never deletes anything, and you
   act on a finding by editing the test file yourself.

## Reading a constraint

A judgement point carries the module's own words. Read them and produce a
conforming value — **never weaken the constraint to fit a value you already
have.** Changing `can(cidrnetmask(var.x))` into something that accepts
`"placeholder"` does not characterise the module; it characterises a module
nobody has.

| Constraint shape | What to produce |
| --- | --- |
| `can(regex("^ami-[0-9a-f]{8}$", var.x))` | A string the pattern matches: `"ami-0123abcd"`. Read the anchors and the length. |
| `can(cidrnetmask(var.x))` / `cidrsubnet(...)` | A real CIDR block: `"10.0.0.0/16"`. Match the prefix length the module's arithmetic needs. |
| `length(var.x) > n`, `length(var.x) <= n` | A value inside every bound at once. Check for a second validation before answering. |
| `alltrue([for v in var.x : ...])` | A collection whose every element satisfies the inner condition — usually one element is enough. |
| A `precondition` over two variables | Answer both together; a satisfying assignment for one alone will fail. |
| Anything you cannot satisfy honestly | Say so, and leave the judgement point open. An open point is a true report; a wrong answer is a suite that pins nonsense. |

## What to add that the tool cannot

- **Day-2 scenarios.** A generated scenario characterises a create from empty
  state. Update and replace behaviour is invisible to it, and *which* input
  change models realistic drift is judgement. Write an ordinary `run` block
  with the inputs you think matter, and assert in it yourself.
- **Discriminating inputs.** When a survivor is diagnosed as unobservable
  under the current inputs, the gap is the *inputs*, not the assertions. Add
  a run block with the inputs you think discriminate the behaviour, then rerun
  the loop and read whether the diagnosis changed.

**A run block you write is executed, not harvested.** This version pins only
the scenarios it planned: `tf-mut` maps a harvest back to a scenario by the
name it generated, so a hand-written run contributes to the mutation grade and
to `curate`'s evidence, and never to `characterisation.pins`. Assertions in it
are yours to write and yours to maintain.

## What the tool refuses, and why you should not talk it round

- **A provider configuration with no mock.** Every configuration — the
  default and each alias — needs one, because Terraform matches mocks to
  configurations by alias. The refusal names the configuration.
- **Provisioners and unsevered data sources.** Mocking severs a provider; it
  does not sever a `local-exec`, an `external` data source or a
  `terraform_remote_state` read. These execute for real under an apply-mode
  scenario.
- **A partial population for `curate` or `--until-dry`.** A redundancy finding
  or a convergence claim drawn from a scoped or sampled run is a false one.
  This refusal has **no opt-in**: drop the count lever and run again.

The first two have an opt-in flag — `--allow-real-infrastructure` and
`--allow-unsandboxed-effects`. **Neither is yours to grant.** Report the
refusal verbatim to the module owner, with what the flag would permit, and
wait for their decision. These gates stand between a test run and a real API
call or a real side effect on the machine it runs on, and an agent that talks
itself round one has removed the only thing standing there.

## Walkthrough

The blocks below are executed in order, exactly as written, by this
repository's end-of-MVP gate against a fixture module. If you change a flag
here and the gate stays green, the gate is not reading these instructions.

```tf-mut-transcript
characterise --reporter json .
todos --reporter json .
characterise --until-dry --reporter json .
characterise --write .
curate --reporter json .
```

Step 3 has its own block, executed against a module with an open judgement
point, because a module that needs no answer cannot demonstrate answering one.
`<todo-id>` is the identifier `tf-mut todos` printed on the line before, and
the value is what this document's constraint table says to produce for
`can(cidrnetmask(var.x))`. A scripted answer travels on every invocation that
needs it, including the write.

```tf-mut-transcript-todo
characterise --reporter json .
todos --reporter json .
characterise --answer <todo-id>="10.0.0.0/16" --reporter json .
characterise --answer <todo-id>="10.0.0.0/16" --write .
```
