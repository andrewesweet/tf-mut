# Issue #87 context reconciliation

Issue #86's dependency rule says that core/supporting contexts and Terraform
Boundary do not depend on Publication, CLI, or renderers, while its shorthand
"Publication imports no context" is broader than the enforceable package boundary.

The six-context table is retained exactly. The enforceable rule is narrowed as
follows: `internal/report` is the stdlib-only publication leaf with no domain-context
imports; `internal/skill` remains grouped under Publication for shipped-document
ownership, but #91 explicitly requires it to call the shared
`internal/sandbox.WriteFreshChecked` filesystem primitive. That is the one planned
Publication-to-Terraform-Boundary exception. Issue #91 remains out of scope for #87.
