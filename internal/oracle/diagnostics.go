package oracle

// Position is a point in a source file, as the Oracle context records it. The
// engine projects it onto the published wire spelling at the boundary.
type Position struct {
	Line   int
	Column int
}

// Range is a source range the Oracle context records. The engine projects it
// onto the published wire spelling at the boundary.
type Range struct {
	File  string
	Start Position
	End   Position
}

// Diagnostic is a Terraform diagnostic a terminal outcome carries: the
// evaluation failure a KilledByError or Invalid mutant is claimed from. The
// engine hands the runner's diagnostics to the constructor, and the projection
// writes them onto the published mutant field for field, so the outcome — not
// the DTO — is what the state's evidence flows through.
type Diagnostic struct {
	Severity string
	Summary  string
	Detail   string
	// Range is nil where Terraform attached no location to the diagnostic.
	Range *Range
	// TestFile and TestRun locate the run the diagnostic was produced under,
	// where Terraform named one.
	TestFile string
	TestRun  string
}
