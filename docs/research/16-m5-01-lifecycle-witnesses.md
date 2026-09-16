# M5-0.1 — the lifecycle kill-witness measurement

The measurement that decides which Tier 4 lifecycle operators M5a admits, run on 17 September
2026 against Terraform v1.15.8 on linux_amd64, offline, builtin provider only. Every verdict
below is [verified] by running the pinned binary; none is taken from documentation.

Five operators are measured — `LC-CBD-FLIP`, `LC-PREVENT-DESTROY-FLIP`, `LC-IGNORE-DROP`,
`LC-IGNORE-ALL`, `LC-REPLACE-TRIGGER-DROP` — over the spec's five run-block shapes (a)–(e).
Per operator and shape, four things are recorded as separate columns: the raw payload paths
that differ between the original and the mutant's verbose test runs; which of those are
assertion-reachable; the canonical delta the oracle would compute through the unchanged
projection; and the kill witness, if one exists — original baseline-green, mutant killed, with
the assertion or error named. Payload difference and canonical delta are recorded beside the
witness and are not witnesses: a difference no assertion can reach is diagnosis material, not
a kill.

## Method

One module template serves every cell: a `terraform_data.trigger` keyed on `var.revision`, a
`terraform_data.subject` with `input = var.value` and `triggers_replace` on the guarded
attribute, the operator's lifecycle body under test, and a `subject_id` output. Each operator
contributes one original body and one mutant body; each shape contributes its run blocks.
Cells were driven through the engine's own `tfexec` runner with its environment stripping, so
the binary, flags and stream decoding are the ones production uses. For every cell the
original suite ran twice (the oracle's own calibration protocol — the volatile mask is
`Derive` over the two baseline legs) and the mutant once, all with `-verbose -json`, and the
canonical projection ran unchanged over the decoded payloads. The static volatility scan is
empty by construction: the template contains no impure call, so the mask is exactly what the
two baseline legs disagree on.

**Raw paths and UUID churn.** Two independently applied runs never share ids, so every cell's
raw diff carries churn on the id paths — `outputs.subject_id.value`,
`root_module.resources[terraform_data.subject].values.id`,
`root_module.resources[terraform_data.trigger].values.id`, and where the plan leg carries
known ids, `resource_changes[…].change.before.id` and `change.after.id`. The per-cell volatile
masks mark exactly these paths (see the mask table below); the raw-difference column below
lists only the paths that differ *beyond* the churn, which is where any operator-specific
signal lives.

**Reachability probes [verified].** An assertion may read resource attributes
(`terraform_data.subject.input`, `.output`, `.id`, `.triggers_replace`), run outputs
(`run.first.subject_id`), and `expect_failures` targets. Probes confirmed the unreachable
side: `terraform_data.subject.change.actions[0]` and `terraform_data.subject.action_reason`
each evaluate to `Unsupported attribute` in both plan and apply run blocks — the whole
`change`/`action_reason` plan-record surface is outside the assertion language.
`change.after_unknown` and `output_changes` are plan-JSON bookkeeping records with no
assertion address at all. `after.input`, `after.output`, state `values.input`/`values.output`
and `id` are readable — the witnesses below read them.

## Shapes (a)–(e), quoting the spec

> (a) create-only plan run; (b) apply run, then plan run sharing a `state_key` with the
> guarded attribute changed; (c) apply run, then plan run with
> `plan_options { replace = [...] }`; (d) apply run and the automatic teardown; (e) apply
> run, then apply run sharing a `state_key`.

For (b) and (e) the second run's `variables` block carries the change — `value = "new"` for
the ignore pair (whose `triggers_replace` is the literal `"fixed"`), `revision = "v2"` for the
replace-pair (whose change rides `triggers_replace = var.revision`).

## LC-CBD-FLIP — `create_before_destroy = true` → `false`

Original replaces on the second run (revision change) and plans Create-then-Delete; the
mutant plans Delete-then-Create. That action ordering is the entire observable difference.

| Shape | Raw differing paths (beyond UUID churn) | Assertion-reachable | Canonical delta the oracle would compute | Kill witness |
| --- | --- | --- | --- | --- |
| (a) | none | — | empty | none — identical create-only plans |
| (b) | `change.actions[0]` `"create"`→`"delete"`, `change.actions[1]` `"delete"`→`"create"` | **no** — `Unsupported attribute` [verified probe] | `change.actions[0]`, `change.actions[1]` — both unreachable | none |
| (c) | as (b) | no | as (b) | none |
| (d) | none | — | empty | none — teardown applies identically |
| (e) | none beyond churn (state payloads carry no change record; ids masked) | — | empty | none |

Rejected probe: `assert { condition = terraform_data.subject.change.actions[0] == "create" }`
reds the baseline — `Unsupported attribute` on both legs, so no suite can distinguish the
legs through any attribute surface. **Decision: not admitted.** Row annotated
"no kill witness under shapes (a)–(e), Terraform v1.15.8"; it reopens on a witness.

## LC-PREVENT-DESTROY-FLIP — `prevent_destroy = true` → `false`

The natural witness shape — plan or apply a replacement of the guarded resource — reds the
*original*: the baseline's second run fails with `Instance cannot be destroyed`
[verified]. A grading baseline that is red is a refusal, not a comparison: the engine refuses
before any oracle work, so no canonical delta exists for (b), (c) or (e). The mutant side is
green everywhere, including the shapes where the original is red.

| Shape | Raw differing paths (beyond UUID churn) | Assertion-reachable | Canonical delta the oracle would compute | Kill witness |
| --- | --- | --- | --- | --- |
| (a) | none | — | empty | none — creation destroys nothing |
| (b) | none (second run has no baseline payload: baseline errored) | — | not comparable — baseline red, engine refuses | none — baseline red: `Instance cannot be destroyed` |
| (c) | as (b) | — | as (b) | none — baseline red, same error |
| (d) | none | — | empty | none — **[verified]** teardown destroys the subject and `prevent_destroy` does not block it; both legs green |
| (e) | as (b) | — | as (b) | none — baseline red on the second apply |

Rejected probes: `expect_failures = [terraform_data.subject]` on the second run does not
rescue any shape — the original's failure is a plan/apply error, not an expectation failure,
so the baseline stays red; under (e) the mutant then fails its own suite because the declared
expectation is unsatisfied (exit 1, `fail`, no diagnostic). Two red legs make no witness.
**Decision: not admitted.** Annotated as above; it reopens on a witness.

## LC-IGNORE-DROP — `ignore_changes = [input]` → block removed

Original holds `input` at `old` across the change; the mutant updates it in place to `new`.

| Shape | Raw differing paths (beyond UUID churn) | Assertion-reachable | Canonical delta the oracle would compute | Kill witness |
| --- | --- | --- | --- | --- |
| (a) | none | — | empty | none |
| (b) | `change.actions[0]` `"no-op"`→`"update"`; `change.after.input` `"old"`→`"new"`; `change.after.output` `"old"`→unknown; `change.after_unknown` `{}`→absent; `change.after_unknown.output` absent→`true` | **yes**: `after.input` (readable); `after.output` readable in principle but unknown in the mutant leg; `actions`/`after_unknown` not | `actions[0]` (unreachable), `after.input`, `after.output`, `after_unknown`, `after_unknown.output` | **yes, (b)** — second run asserts `terraform_data.subject.input == "old"`; original 2 passed, mutant `Killed` (`Test assertion failed`) [verified] |
| (c) | none beyond churn — both legs force the same replace | — | empty | none needed |
| (d) | none | — | empty | none |
| (e) | state `values.input` `"old"`→`"new"`, `values.output` `"old"`→`"new"` | **yes** — both readable | `values.input`, `values.output` | **yes, (e)** — second apply asserts `input == "old"`; mutant `Killed` [verified] |

**Decision: admitted** under shape (b), with (e) as a second witnessed shape. The canonical
delta at the witnessed shape lies on `after.input` — a reachable member — so the oracle-slice
filing rule below does not fire.

## LC-IGNORE-ALL — `ignore_changes = [triggers_replace]` → `ignore_changes = all`

Mirror image of the drop: the original updates `input` to `new` (one attribute ignored does
not include `input`), the mutant holds everything at `old`.

| Shape | Raw differing paths (beyond UUID churn) | Assertion-reachable | Canonical delta the oracle would compute | Kill witness |
| --- | --- | --- | --- | --- |
| (a) | none | — | empty | none |
| (b) | `change.actions[0]` `"update"`→`"no-op"`; `after.input` `"new"`→`"old"`; `after.output` unknown→`"old"`; `after_unknown` absent→`{}`; `after_unknown.output` `true`→absent | **yes**: `after.input`, `after.output`; the rest not | as ignore-drop, directions reversed | **yes, (b)** — second run asserts `terraform_data.subject.input == "new"`; mutant `Killed` [verified] |
| (c) | none beyond churn | — | empty | none needed |
| (d) | none | — | empty | none |
| (e) | state `values.input` `"new"`→`"old"`, `values.output` `"new"`→`"old"` | **yes** | `values.input`, `values.output` | **yes, (e)** — assertion `input == "new"` in the second apply kills [verified] |

**Decision: admitted** under shape (b), with (e) as a second witnessed shape; delta on a
reachable member; filing rule does not fire.

## LC-REPLACE-TRIGGER-DROP — `replace_triggered_by = [terraform_data.trigger]` → block removed

Original replaces the subject when the trigger's input changes; the mutant updates the
trigger and leaves the subject alone. Under (b) the plan legs differ richly — but the
readable members are unknowable exactly where the behaviour differs, and the probe
established the review's prior [verified]: an `id`-inequality assertion reds the *baseline*
with `Unknown condition value`, because the original's replacement leaves `after.id` unknown.
Under (e) both legs are real applies and the assertion compares two known ids from the same
invocation — that is the witness.

| Shape | Raw differing paths (beyond UUID churn) | Assertion-reachable | Canonical delta the oracle would compute | Kill witness |
| --- | --- | --- | --- | --- |
| (a) | none | — | empty | none |
| (b) | `output_changes.subject_id.actions[0]` `"update"`→`"no-op"`, `.after` absent→id, `.after_unknown` `true`→`false`; `action_reason` `"replace_by_triggers"`→absent; `change.actions[0]` `"delete"`→`"no-op"`, `[1]` `"create"`→absent; `change.after.id` unknown→known; `change.after.output` unknown→`"old"`; `after_unknown` absent→`{}`; `after_unknown.id` `true`→absent; `after_unknown.output` `true`→absent | **no, effectively** — `action_reason` and `change.actions` are `Unsupported attribute` [verified probe]; `output_changes`/`after_unknown` have no assertion surface; `after.id` is readable *in principle* but unknown in the baseline leg, so any comparison errors with `Unknown condition value` [verified probe] | the eleven paths above, masked where both baseline legs are no-ops | **none** — baseline red: `Unknown condition value` |
| (c) | none beyond churn — both legs force the replace | — | empty | none needed |
| (d) | none | — | empty | none |
| (e) | second run's state `values.id` and `outputs.subject_id.value` differ — the mutant keeps the first apply's id, the original does not — **all masked**: ids are independently generated per leg | the paths *are* readable within one invocation — that is what the witness reads | **empty** | **yes, (e)** — second apply asserts `terraform_data.subject.id != run.first.subject_id`; original 2 passed, mutant `Killed` (`Test assertion failed`) [verified] |

**Decision: admitted** under shape (e) only. The empty canonical delta at the witnessed shape
is a finding worth stating plainly: the oracle's cross-leg comparison masks the subject id as
volatile (correctly — two legs never share a UUID), so had the mutant survived phase one the
oracle would have called the payloads fingerprint-identical. It does not survive phase one:
the assertion compares both ids *within* one invocation, where the difference is real, and
kills. The phase-one assertion and the cross-leg oracle ask different questions, and this
operator is the case where the distinction decides admission.

## Admission decisions

| Operator | Witnessed shape(s) | Killing assertion / error | Decision |
| --- | --- | --- | --- |
| `LC-CBD-FLIP` | none | — | **not admitted**; row annotated |
| `LC-PREVENT-DESTROY-FLIP` | none | — | **not admitted**; row annotated |
| `LC-IGNORE-DROP` | (b), (e) | `input == "old"` in the second run | **admitted to `deep` in M5a** |
| `LC-IGNORE-ALL` | (b), (e) | `input == "new"` in the second run | **admitted to `deep` in M5a** |
| `LC-REPLACE-TRIGGER-DROP` | (e) | `id != run.first.subject_id` in the second apply | **admitted to `deep` in M5a** |

The admitted operators carry their witnessed shape and assertion forward as the matrix row's
"Kills when" and fix text when M5a adds the rows; until that change, the catalogue enables
nothing and the applicability matrix is untouched.

## The reachability filing rule, applied

Rule: an *admitted* operator whose canonical delta at its witnessed shape lies solely on
members no test assertion can read specifies an oracle slice in its own issue, unbuilt in M5.
Measured outcome: **no admitted operator needs it.**

- `LC-IGNORE-DROP` / `LC-IGNORE-ALL` at (b): the delta carries `after.input` — reachable, and
  the witness reads it. At (e): `values.input`/`values.output`, both reachable.
- `LC-REPLACE-TRIGGER-DROP` at (e): the delta is empty; the recorded-beside payload
  difference sits on id paths that are readable within an invocation — the witness reads
  them. Nothing unreachable-only.

The unadmitted operators do have unreachable-only deltas — `LC-CBD-FLIP`'s
`change.actions` pair under (b)/(c) is exactly that shape — but the rule licenses nothing for
unadmitted operators beyond publishing the finding, which this document does.

## Volatile masks as measured

Per shape, the paths the two-baseline-legs `Derive` marks volatile (the static scan
contributes nothing):

| Shape | Volatile paths |
| --- | --- |
| (a) | none — the create-only plan leg is identical across invocations |
| (b), (c) | `outputs.subject_id.value`; both resources' `values.id`; `change.before.id` of both resources; `output_changes.subject_id.before`; plus `change.after.id` and `output_changes.subject_id.after` of whichever resource keeps a known id across legs — under the ignore pair and the replace pair's *mutant*, `after.id` is a no-op id and volatile; under the replace pair's *original* it is unknown in both legs and equal-by-absence |
| (d), (e) | `outputs.subject_id.value`; both resources' `values.id` |

## Reproduction

The three admitted witnesses are committed as offline fixtures —
[`research/spikes/m5-lifecycle/`](../../research/spikes/m5-lifecycle/) — one pair per
operator, `original/` and `mutant/`, `terraform_data` only, no init required:

```bash
cd research/spikes/m5-lifecycle/ignore-drop/original  && terraform test  # exit 0
cd ../mutant                                          && terraform test  # exit 1, Test assertion failed
```

Each pair reproduces the recorded witness: original baseline-green, mutant killed by the
named assertion under the named shape. The rejected probes quoted above are one-line
assertion edits against the same template.

Nothing in the canonical projection, the oracle precedence or any existing verdict changed to
produce this document; the measurement ran the shipped code paths with no product edits.
