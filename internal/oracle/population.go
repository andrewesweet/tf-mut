package oracle

import (
	"fmt"
	"strconv"
	"strings"
)

// The population fact. The gate table's Full row is a claim a run has to
// earn, and until this value existed the earning was re-derived from the
// published report at each call site: curate checked observation in one file,
// the baseline writer recomputed the row's shape in another, and the
// until-dry loop — whose dry claim is the strongest conclusion the tool
// draws — checked nothing at all. Here the claim is a value owned by the
// context that classified the population. A consumer that draws a conclusion
// over a population receives one that earned it, the population and its
// proof travel together, and the only route to either value is a constructor
// that refuses everything else.

// Provenance records how a classified population was produced: the counts the
// gates judge the population by. They are the run's own observations about
// its population — what was replayed and what was left unobserved — carried
// beside the classified rows so no consumer has to re-derive them from a
// published projection.
type Provenance struct {
	// Cached is the number of selected mutants whose verdicts were replayed
	// from the verdict cache.
	Cached int
	// TimedOut is the number of mutants that exceeded their execution budget,
	// whose kills were therefore never observed.
	TimedOut int
	// Errored is the number of mutants that could not be evaluated at all.
	Errored int
	// Unexecuted is the number of selected mutants the run stopped before
	// dispatching: the consecutive-failure trip left them unobserved.
	Unexecuted int
	Tripped    bool
}

// Population is the classified population of one run: every outcome the
// oracle assigned, paired with the operator that generated its mutant, plus
// the provenance the gates judge it by. It is the fact the metrics
// arithmetic, the baseline gate, curate's findings and the until-dry
// convergence claim are all drawn from.
//
// The fields are unexported: the value exists only where NewPopulation
// assembled it from the classification sites, and its proofs exist only
// where Authoritative and Fresh declined to refuse.
type Population struct {
	classified []Classified
	provenance Provenance
}

// NewPopulation is the constructor: the classified rows the run's
// classification sites produced, with the provenance counts observed along
// the way. It refuses nothing — an unobserved population is a legal
// population, and refusal is the proofs' job — so a caller that never earns
// Authoritative can hold the fact without being able to draw a conclusion
// from it.
func NewPopulation(classified []Classified, provenance Provenance) Population {
	return Population{classified: classified, provenance: provenance}
}

// Classified returns the classified rows the population holds, in the order
// the run's published mutants carry them.
func (p Population) Classified() []Classified { return p.classified }

// Authoritative is a classified population every member of which was
// observed: no mutant timed out and none failed to evaluate. Both leave
// mutants unobserved, and an unobserved mutant is not an absent one — an
// empty kill set drawn over mutants that never ran is a false finding of
// exactly the kind this value exists to make unconstructible.
//
// The zero value proves nothing and no consumer accepts it: the value exists
// only where Population.Authoritative declined to refuse.
type Authoritative struct {
	// population is the classified population the proof was made from.
	population Population
}

// Authoritative is the observation check the proof-consuming commands share,
// promoted to the one route to the value.
//
// The rule itself — which populations count as fully observed, and why — is
// stated once, here. refused is the sentinel of the rule the caller serves
// and rationale is that command's own remedy: the reasons are
// command-neutral, the refusal is spelled in the refusing command's terms.
func (p Population) Authoritative(refused error, rationale string) (Authoritative, error) {
	reasons := []string{}

	if p.provenance.TimedOut > 0 {
		reasons = append(reasons,
			strconv.Itoa(p.provenance.TimedOut)+" mutant(s) timed out, so their kills were never observed")
	}

	if p.provenance.Errored > 0 {
		reasons = append(reasons,
			strconv.Itoa(p.provenance.Errored)+" mutant(s) could not be evaluated at all")
	}

	if p.provenance.Unexecuted > 0 {
		reasons = append(reasons,
			strconv.Itoa(p.provenance.Unexecuted)+" mutant(s) were left unexecuted, so they were never observed")
	}

	if len(reasons) != 0 {
		return Authoritative{}, fmt.Errorf("%w: %s\n%s",
			refused, strings.Join(reasons, "; "), rationale)
	}

	return Authoritative{population: p}, nil
}

// Classified returns the classified population the proof covers.
func (a Authoritative) Classified() []Classified { return a.population.classified }

// Fresh refines an authoritative population with freshness: no verdict in it
// was replayed from the cache. It is the strictly narrower proof the baseline
// writer demands — staleness and a rewrite are claims about what actually
// ran — and it exists only on top of a proof of observation, never instead of
// one.
//
// The zero value proves nothing and no consumer accepts it: the value exists
// only where Authoritative.Fresh declined to refuse.
type Fresh struct {
	// authoritative is the observation proof the freshness claim refines.
	authoritative Authoritative
}

// Fresh refines a proven population with freshness: it refuses anything that
// replayed even one verdict from the cache, with the cache row's own remedy.
// refused is the sentinel of the rule the caller serves — the baseline
// write's, in the one consumer there is today.
func (a Authoritative) Fresh(refused error) (Fresh, error) {
	if cached := a.population.provenance.Cached; cached > 0 {
		return Fresh{}, fmt.Errorf("%w: this run replayed %d cached verdict(s); use --no-cache",
			refused, cached)
	}

	return Fresh{authoritative: a}, nil
}

// Classified returns the classified population the freshness proof covers.
func (f Fresh) Classified() []Classified { return f.authoritative.population.classified }
