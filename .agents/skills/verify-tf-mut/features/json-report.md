# json-report

Every reporting command can emit the JSON report: one report value, projected to a
versioned schema, that later tooling and ship briefs cite as evidence.

## Sub-features

- `json-schema` stamps a `schema_version` on every report.
- `json-metrics` carries the machine fields behind the terminal summary.
- `json-provenance` names the module, the terraform version and the selection the run used.
- `json-sidecar` writes the JSON artifact alongside the terminal reporter from one run.

## How to get to it (user POV)

- Run `tf-mut <command> --reporter json PATH` to replace the terminal reporter, or
  `tf-mut <command> --output json=PATH PATH` to add the JSON artifact next to it.

## Driving it with tf-mut

Preconditions:

- Doctor is green and `$BIN` is the fresh binary.
- `$VERIFY_ROOT/modules/all-killed` is a fixture copy.

- **Emit the report.** Run
  `mise exec -- "$BIN" run --no-cache --output json=$EVIDENCE/run-all-killed.json "$VERIFY_ROOT/modules/all-killed"`.
  Exit code `0`; the artifact exists.
- **Assert the contract.** `mise exec -- jq -e '.schema_version | test("^[0-9]+[.][0-9]+[.][0-9]+$")'
  "$EVIDENCE/run-all-killed.json"` succeeds; `.terraform_version` starts with `1.15.`;
  `.metrics.mutation_score == 1`; `.metrics.scored == 3`; `.metrics.counts.Killed == 3`.
- **Provenance is present.** The same artifact has non-empty `.module` and `.closure_root`
  and a `.selection` object — the run says what it ran against.
- **Terminal and JSON agree.** `.metrics.scored` equals the scored count in the terminal
  summary's `(3 of 3 scored mutants detected)` from the same invocation.

## Gotchas

- `schema_version` moves with intentional schema changes; assert its shape, not a pinned
  value — unless the change you are verifying is itself the schema change.
- Characterisation commands (`characterise`, `todos`, `curate`) accept only the `terminal`
  and `json` reporters; SARIF, HTML, MTE, JUnit and markdown belong to `run`, `preview`
  and `suggest` only.
