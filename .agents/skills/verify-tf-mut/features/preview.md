# preview

`tf-mut preview` lists the mutants a run would generate — operator, site and diff — and
executes nothing. It is the dry-run surface for the mutation catalogue.

## Sub-features

- `preview-count` reports how many mutants the catalogue generates for a module.
- `preview-diff` renders each mutant as a minimal diff confined to the tokens the operator
  owns.
- `preview-static` runs no suite: a preview never executes Terraform tests.

## How to get to it (user POV)

- Run `tf-mut preview [flags] PATH`.

## Driving it with tf-mut

Preconditions:

- Doctor is green and `$BIN` is the fresh binary.
- `$VERIFY_ROOT/modules/all-killed` is a fixture copy.

- **Count the population.** Run `$BIN preview "$VERIFY_ROOT/modules/all-killed"`. Exit code
  `0`; stdout carries `3 mutants would be generated` and lists the operator IDs
  `EXT-OUTPUT-NULL`, `STR-CASE` and `STR-EMPTY`.
- **Read a diff.** The same output shows, under the `EXT-OUTPUT-NULL  main.tf:1  output.tier`
  heading, the pair `-  value = "standard"` / `+  value = null` — one operator-owned change,
  not a whole-file diff.
- **Persist the listing.** Run
  `$BIN preview "$VERIFY_ROOT/modules/all-killed" > $EVIDENCE/preview-all-killed.txt`. The
  artifact contains all three operator IDs.

## Gotchas

- A preview is static: if it prints test-run output or takes more than moments, the binary
  or the invocation is wrong.
- The mutant list is content-derived and stable for identical fixture bytes; upstream
  fixture edits change the expected count — re-derive it from `preview` before asserting.
