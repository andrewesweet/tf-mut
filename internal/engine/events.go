package engine

import (
	"github.com/andrewesweet/tf-mut/internal/oracle"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/suggest"
)

// Engine-owned domain events for grading and characterisation. ADR-0005 owns
// their payload, delivery and immutability contract.

// MutantEvaluated is one executed mutant's result: its published row, carrying
// the evaluation's telemetry record, the outcome the oracle classified, and
// the operational failure, if the evaluation had one. index is the row's slot
// in the population.
type MutantEvaluated struct {
	index      int
	row        report.Mutant
	classified oracle.Classified
	failure    *report.ExecutionError
}

// PopulationClassified is the run's population fact: every selected row has
// an outcome, and the provenance the gates judge it by is fixed.
type PopulationClassified struct {
	population oracle.Population
}

// SuggestionProposed is a dry run's unverified candidate for one survivor.
type SuggestionProposed struct {
	candidate suggest.Candidate
}

// SuggestionVerified is a suggestion both verification legs proved.
type SuggestionVerified struct {
	suggestion suggest.Suggestion
}

// SuggestionRefuted is a suggestion verification refused.
type SuggestionRefuted struct {
	suggestion suggest.Suggestion
}

// SuggestionSkipped is a survivor generation or verification had nothing
// honest to suggest for, with its reason.
type SuggestionSkipped struct {
	suggestion suggest.Suggestion
}

// VerificationCosted is what verification actually executed, raised once
// after the suggestion leg's last outcome.
type VerificationCosted struct {
	costs verificationCosts
}

// SuiteCharacterised is the characterisation's concluded record, raised once
// whether the suite was written, staged or the until-dry loop was refused.
type SuiteCharacterised struct {
	suite report.Characterisation
}

// topic delivers one event type to its subscribers synchronously, on the
// publisher's goroutine, in subscription order. The first subscriber error
// stops delivery and is returned to the publisher, which fails the command
// with it. A subscriber may publish: the nested event is delivered in full
// before the outer delivery moves on.
type topic[E any] struct {
	subscribers []func(E) error
}

func (t *topic[E]) subscribe(subscriber func(E) error) {
	t.subscribers = append(t.subscribers, subscriber)
}

func (t *topic[E]) publish(event E) error {
	for _, subscriber := range t.subscribers {
		if err := subscriber(event); err != nil {
			return err
		}
	}

	return nil
}

// dispatcher is one command's event topics. It holds no goroutine, queue or
// buffer: publication returns when every subscriber has reacted.
type dispatcher struct {
	mutantEvaluated      topic[MutantEvaluated]
	populationClassified topic[PopulationClassified]
	suggestionProposed   topic[SuggestionProposed]
	suggestionVerified   topic[SuggestionVerified]
	suggestionRefuted    topic[SuggestionRefuted]
	suggestionSkipped    topic[SuggestionSkipped]
	verificationCosted   topic[VerificationCosted]
	suiteCharacterised   topic[SuiteCharacterised]
}
