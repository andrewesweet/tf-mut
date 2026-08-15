# HCL2 tooling and the mutable surface of the Terraform language

## 1. Why `hashicorp/hcl/v2` and not a third-party parser

Terraform parses configuration with `github.com/hashicorp/hcl/v2`. Every other parser —
`python-hcl2`, `hcl2-parser`, tree-sitter grammars — reimplements the grammar and therefore
diverges from it. Oasis hit this directly and documented it: `python-hcl2` rejects some `for`
expressions that Terraform accepts, so using it as a validity gate silently drops real files.
Their fix was to abandon AST parsing and mutate with regexes. The better fix is to use the
same library Terraform uses, where the divergence cannot exist by construction.

This makes **Go the natural implementation language**. It also gives access to
`hashicorp/terraform`'s own `internal/configs` semantics if deeper analysis is ever needed,
and produces a single static binary with no runtime dependency — the distribution model the
Terraform ecosystem expects.

### The two halves of the library

| Package | Role |
| --- | --- |
| `hclsyntax` | The real parser. Produces a semantic AST (`hclsyntax.Body`, `hclsyntax.Expression`) with accurate `hcl.Range` source positions. This is what we *analyse*. |
| `hclwrite` | A parallel, token-oriented AST for surgical rewriting. Comments and whitespace are preserved where untouched. This is what we *mutate*. |

`hclwrite` works by parsing with `hclsyntax` and then using the AST's source ranges to
partition the raw token stream, matching tokens to nodes. Its API is deliberately DOM-like:
nodes represent syntax constructs, not semantic concepts, and can be read, created and
inserted. `File.WriteTo` re-emits the tokens with a formatting pass that only adjusts spacing.

The practical consequences for a mutation engine:

- **Mutants are minimal diffs.** Only the mutated tokens change; the rest of the file is
  byte-identical. Reports show a one-line unified diff rather than a reformatted file.
- **Source positions are exact.** Every mutant carries file, line and column, which is what
  SARIF and PR annotations need.
- **Round-tripping is safe.** Parse → mutate → write on an unmutated file is a no-op, which is
  a cheap and strong self-test for the engine.

One caveat worth designing around: `hclwrite`'s expression-level API is thinner than its
block/attribute-level API. Some expression mutations are most reliably done by locating the
sub-expression's `hcl.Range` via `hclsyntax` and splicing tokens at that range. The engine
should be built around a `(range, replacementTokens)` primitive, with the higher-level
`hclwrite` helpers used where they fit.

## 2. The HCL native-syntax grammar — the mutable surface

Every construct below is a mutation opportunity. This is taken from the HCL native syntax
specification and is the basis of the language-level operator catalogue.

### Operators and precedence

```
unary:        -  !                              (highest)
level 6:      *  /  %
level 5:      +  -
level 4:      >  >=  <  <=
level 3:      ==  !=
level 2:      &&
level 1:      ||                                (lowest)
```

Left-associative within each level. This precedence table is what tells the mutation engine
when a replacement needs parenthesising to preserve the original tree shape — swapping `&&`
for `||` is safe in place, but replacing a sub-expression with a lower-precedence one is not.

### Expression forms

| Form | Grammar | Mutation handles |
| --- | --- | --- |
| Literal | `NumericLit \| "true" \| "false" \| "null"` | Value substitution, boolean flip, off-by-one, null injection |
| Tuple | `"[" expr ("," \| newline) ... "]"` | Drop first / drop last / empty |
| Object | `"{" (Identifier \| Expression) ("=" \| ":") Expression ... "}"` | Drop entry, duplicate key, empty |
| Quoted template | `'"' (literal \| interpolation \| directive)* '"'` | Drop an interpolation, alter a literal segment, flip strip markers |
| Heredoc | `("<<" \| "<<-") Ident newline ... Ident` | Same as quoted, plus indentation-strip flip |
| Template `if` | `%{if e} ... %{else} ... %{endif}` | Negate predicate, collapse to one branch |
| Template `for` | `%{for x in e} ... %{endfor}` | Empty the collection |
| Variable | `Identifier` | Reference swap (same-type siblings) |
| Function call | `Identifier "(" args ("," \| "...")? ")"` | Function substitution, argument reorder, argument drop, expansion (`...`) removal |
| For-tuple | `"[" "for" x ("," y)? "in" e ":" v (if c)? "]"` | Remove `if`, negate `if`, swap iteration variables |
| For-object | `"{" "for" ... ":" k "=>" v "..."? (if c)? "}"` | As above, plus grouping-mode (`...`) toggle, key/value swap |
| Index | `expr "[" expr "]"` | Index shift |
| GetAttr | `expr "." Identifier` | Attribute swap |
| Attr splat | `expr ".*" GetAttr*` | Convert to index-0, i.e. `xs.*.id` → `[xs[0].id]` |
| Full splat | `expr "[*]" (GetAttr \| Index)*` | As above |
| Conditional | `Expression "?" Expression ":" Expression` | Swap branches, negate predicate, collapse to either branch |

Two grammar details with direct operator consequences:

- Splat expressions **auto-wrap non-collection scalars** and **yield an empty tuple for null**.
  So `null` versus `[]` versus a scalar are three genuinely distinct behaviours a test may or
  may not distinguish — a good mutation target.
- A conditional's predicate must be boolean and its two arms must unify to a common type. That
  constrains branch-collapse mutants: collapsing to one arm is always type-safe, whereas
  substituting an arbitrary expression may not be.

## 3. Terraform-level structure above HCL

HCL is only the syntax. Terraform's own block schema adds the constructs that carry most of a
module's actual behaviour, and these are where the highest-value mutations live:

| Construct | Behavioural weight |
| --- | --- |
| `count`, `for_each` | Determines how many resource instances exist — almost always assertable |
| `dynamic` blocks | Determines how many nested blocks exist |
| `depends_on` | Ordering only; frequently unassertable, so a good pseudo-test detector |
| `lifecycle { create_before_destroy, prevent_destroy, ignore_changes, replace_triggered_by }` | Rarely tested at all |
| `lifecycle { precondition, postcondition }` | Contract surface; directly targetable by `expect_failures` |
| `variable { type, default, nullable, sensitive, validation }` | The module's public contract |
| `output { value, sensitive, depends_on, precondition }` | The module's observable surface — mutations here are almost always killable |
| `check` blocks | Assertion surface |
| `locals` | Pure computation; the closest thing Terraform has to "business logic" |
| `module` call inputs | Wiring between modules |
| Provider meta-arguments, `provider`/`alias` | Placement |

The `optional(type, default)` modifier inside object type constraints deserves a specific
mention: it embeds default values in the *type system*, and those defaults are almost never
covered by tests. Removing an `optional()` default is a cheap, high-yield mutation.

## 4. Interaction with mocked providers

Because the brief targets fully-mocked unit tests, one property of mocking shapes operator
selection heavily.

A `mock_provider` auto-generates values for computed attributes by type: numbers become `0`,
booleans `false`, strings an 8-character alphanumeric, collections empty. Any mutation whose
only effect is on a *computed* attribute is therefore invisible — the mock overwrites it. But
mutations to *configured* attributes remain fully visible in the plan, because the mock
reports the real provider schema and the plan still records the configured values.

This yields a clean rule:

> Under mocked providers, mutations to **configured** arguments, meta-arguments, locals,
> variables and outputs are observable. Mutations that only affect values the provider would
> have computed are not.

The tool can detect the latter automatically rather than guessing (see the plan-fingerprint
oracle in `04-harness-spike.md`), and should report it as a distinct diagnosis — *mock-masked*
— rather than as an ordinary survivor, because the fix is to add a `mock_resource` default or
an `override_resource`, not to add an assertion.

## Sources

- [`hclwrite` package documentation](https://pkg.go.dev/github.com/hashicorp/hcl/v2/hclwrite)
- [`hclsyntax` package documentation](https://pkg.go.dev/github.com/hashicorp/hcl/v2/hclsyntax)
- [HCL native syntax specification](https://raw.githubusercontent.com/hashicorp/hcl/main/hclsyntax/spec.md)
- [Terraform provider mocking documentation](https://developer.hashicorp.com/terraform/language/tests/mocking)
