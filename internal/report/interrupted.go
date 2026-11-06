package report

// Interrupted records that a report was published for the effects an incomplete
// invocation had already committed (2.20.0).
//
// Cancellation and commitment are different facts. Every command treats an
// interrupted invocation as an operational failure, and a failure that
// discarded the report would leave the writes it had already made on disk
// unexplained: the caller could not tell which requested changes landed. So the
// report is published anyway, carrying this block, and the block's presence is
// the whole claim — the invocation did not complete, the analysis around it is
// not a score, and the writes named here are facts about the filesystem.
//
// It is deliberately not Metrics.Incomplete, which marks a completed run whose
// score is untrustworthy: an interrupted report has no score for that flag to
// qualify.
type Interrupted struct {
	// Reason is the failure that ended the invocation.
	Reason string `json:"reason"`
	// CommittedWrites lists the files the invocation wrote before it was
	// interrupted, in write order.
	CommittedWrites []string `json:"committed_writes,omitempty"`
}

// Interrupt marks the report as interrupted by cause, which must be non-nil,
// and reports whether there was a receipt to publish. Only an invocation that
// committed effects has one:
// a run that changed nothing returns false with the report unchanged, because
// its failure is already the whole explanation and a receipt of nothing is not
// worth printing.
//
// The first interruption wins. The layer that observed it — the run itself, or
// the command line publishing the run's report — sets the reason, and a later
// wrapper cannot overwrite it with a longer message about the same failure.
func (r Report) Interrupt(cause error) (Report, bool) {
	if r.Interrupted != nil {
		return r, true
	}

	committed := r.committedWrites()
	if len(committed) == 0 {
		return r, false
	}

	r.Interrupted = &Interrupted{Reason: cause.Error(), CommittedWrites: committed}

	return r, true
}

// committedWrites lists the files the report records as written. A command that
// gains a write receipt names it here, so this rule covers every command that
// can leave effects behind rather than only the one it was written for.
func (r Report) committedWrites() []string {
	var committed []string
	if r.Gates != nil && r.Gates.Baseline != nil && r.Gates.Baseline.Write == BaselineWritten {
		committed = append(committed, r.Gates.Baseline.Path)
	}
	if r.Apply != nil {
		committed = append(committed, r.Apply.Written...)
	}
	if r.Characterisation != nil && r.Characterisation.Write != nil {
		committed = append(committed, r.Characterisation.Write.Written...)
	}

	return committed
}
