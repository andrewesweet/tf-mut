# todos

`tf-mut todos` lists the judgement points characterisation refuses to guess at — the open
decisions an agent must answer before a generated suite can pin them — together with the
evidence for each.

## Sub-features

- `todos-header` reports the plan's granularity, scenario count and pin count.
- `todos-open` lists open judgement points with their evidence when the plan has any.

## How to get to it (user POV)

- Run `tf-mut todos [flags] PATH`.

## Driving it with tf-mut

Preconditions:

- Doctor is green and `$BIN` is the fresh binary.
- `$VERIFY_ROOT/modules/untested-branches` is a fixture copy.

- **Read the plan.** Run `$BIN todos "$VERIFY_ROOT/modules/untested-branches"`. Exit code
  `0`; stdout carries the plan header `granularity  outputs`, `scenarios` with count `2`,
  and `pinned` with count `0`.
- **Persist the listing.** Run
  `$BIN todos "$VERIFY_ROOT/modules/untested-branches" > $EVIDENCE/todos.txt`. The artifact
  carries the same header.

## Gotchas

- `todos` is read-only over a planned characterisation; it never writes.
- Counts move with the fixture; assert the header shape and the exit code, and treat a
  changed count as a fixture change to re-derive, not a harness failure.
