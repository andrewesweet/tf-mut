package characterise

// The construct scaffold: the entity this context owns, its closed state
// vocabulary, and the transition promotion demands.
//
// A scaffold names a construct the oracle cannot assert on and the shape of
// the check somebody has to answer it with. It is never executable and never
// verified on its own: it exists in exactly one of two states — scaffolded,
// carrying the proposal, or promoted, carrying test content — and the move
// between them runs through Promote alone. The rule "a scaffold whose
// behaviour did not verify cannot become test content" is the transition's
// signature: Promote is the only route to promoted, and the only value it
// accepts is the verification evidence, which the engine builds through
// Verified at the point its verifier succeeded.

// ScaffoldStatus is the closed state vocabulary of a scaffold, in the
// vocabulary the Characterisation context owns. The application layer projects
// it onto the published wire spelling at the report boundary; the strings are
// the wire spellings, and extending the set is a schema-version event, not a
// silently additive change.
type ScaffoldStatus string

// The scaffold states.
const (
	// StatusScaffolded marks non-executable material awaiting an answer.
	StatusScaffolded ScaffoldStatus = "scaffolded"
	// StatusPromoted marks a scaffold whose behaviour verified and which
	// became test content.
	StatusPromoted ScaffoldStatus = "promoted"
)

// kindExpectFailures is the one proposed shape a scaffold carries. There is
// no second kind, so the constructor takes none and nothing can record a
// scaffold of a kind the renderer cannot render.
const kindExpectFailures = "expect_failures"

// Scaffold is non-executable material awaiting an answer before it can become
// test content. Its fields are unexported: a scaffold exists only where
// Scaffolded built it, and its state changes only through the transition.
type Scaffold struct {
	id       string
	kind     string
	address  string
	status   ScaffoldStatus
	artefact string
}

// Scaffolded records a construct the oracle cannot assert on as non-executable
// material awaiting an answer. The identity is a hash over the construct
// address — the address is what the scaffold is about, and two scaffolds for
// one construct are one scaffold. The artefact is the non-executable file the
// scaffold lives in, relative to the module directory.
func Scaffolded(address, artefact string) Scaffold {
	return Scaffold{
		id:       Identify("scf-", address),
		kind:     kindExpectFailures,
		address:  address,
		status:   StatusScaffolded,
		artefact: artefact,
	}
}

// ID returns the scaffold's stable identity.
func (s Scaffold) ID() string { return s.id }

// Kind returns the proposed check's shape.
func (s Scaffold) Kind() string { return s.kind }

// Address returns the construct the scaffold is about.
func (s Scaffold) Address() string { return s.address }

// Status returns the scaffold's state in the closed vocabulary.
func (s Scaffold) Status() ScaffoldStatus { return s.status }

// Artefact returns the non-executable file the scaffold lives in. A promoted
// scaffold has left the artefact — it is test content now — so a promoted one
// carries none.
func (s Scaffold) Artefact() string { return s.artefact }

// Promote moves the scaffold into test content. The evidence is the
// verification that earned it, and it is not optional: this transition is the
// only route to promoted, and a scaffold nobody verified cannot take it. The
// evidence is deliberately not published — the report carries the promoted
// state and nothing else — so the parameter is referenced only to keep the
// contract spelled at the call site.
func (s Scaffold) Promote(evidence Verification) Scaffold {
	// The assignment keeps the contract's parameter referenced without
	// publishing it; nothing a report carries comes from the evidence.
	_ = evidence
	promoted := s
	promoted.status = StatusPromoted
	promoted.artefact = ""

	return promoted
}
