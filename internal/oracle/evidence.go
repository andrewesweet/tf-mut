package oracle

import "github.com/andrewesweet/tf-mut/internal/fingerprint"

// Evidence is what a classification carries behind it, one shape for every
// outcome. Its fields are unexported and populated only by constructors: each
// constructor's parameters are the evidence subset its diagnosis requires, so
// no per-diagnosis evidence type exists and no illegal subset is constructible.
type Evidence struct {
	// delta is the masked observable difference, recorded only by the
	// diagnoses a proven difference produced. A nil delta is "none recorded",
	// which keeps "the path was not there" distinguishable from a recorded
	// difference with no changes.
	delta []fingerprint.Change
	// unknownPaths names the addresses whose value the payload left unknown.
	unknownPaths []string
	// volatileComponents names the payload paths the baseline mask removed.
	volatileComponents []string
	// unstableAttributes names the attributes that differed across the two
	// runs of the mutant itself.
	unstableAttributes []string
	// assertion locates the assertion that read the delta but did not catch it.
	assertion string
	// closureVerdict records what the output and local closure concluded.
	closureVerdict string
	// defeatedBy names the construct that defeated the closure computation.
	defeatedBy string
}

// Delta returns the masked observable difference, or nil where the diagnosis
// records none.
func (e Evidence) Delta() []fingerprint.Change { return e.delta }

// UnknownPaths returns the addresses whose value the payload left unknown.
func (e Evidence) UnknownPaths() []string { return e.unknownPaths }

// VolatileComponents returns the payload paths the baseline mask removed.
func (e Evidence) VolatileComponents() []string { return e.volatileComponents }

// UnstableAttributes returns the attributes that differed across the two runs
// of the mutant itself.
func (e Evidence) UnstableAttributes() []string { return e.unstableAttributes }

// Assertion returns the location of the assertion that read the delta but did
// not catch it, where the diagnosis names one.
func (e Evidence) Assertion() string { return e.assertion }

// ClosureVerdict returns what the output and local closure concluded.
func (e Evidence) ClosureVerdict() string { return e.closureVerdict }

// DefeatedBy returns the construct that defeated the closure computation,
// where one did.
func (e Evidence) DefeatedBy() string { return e.defeatedBy }
