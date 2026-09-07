# Issue #87 context reconciliation

Issue #86's dependency rule is a normative target: core/supporting contexts and
Terraform Boundary should not depend on Publication, CLI, or renderers, while its shorthand
"Publication imports no context" is broader than the enforceable package boundary.

The six-context table is retained exactly as the target of #86. It does not describe
the current package graph: `internal/oracle` does not yet exist, and verdict and
supporting-context migrations remain pending in #101–#103, #105–#109 and #113;
the compliant subset's lint enforcement is #89. The enforceable rule is narrowed
as follows: `internal/report` is the stdlib-only publication leaf with no
domain-context imports; `internal/skill` remains grouped under Publication for
shipped-document ownership, but #91 explicitly requires it to call the shared
`internal/sandbox.WriteFreshChecked` filesystem primitive. That is the one planned
Publication-to-Terraform-Boundary exception. Issue #91 remains out of scope for #87.
