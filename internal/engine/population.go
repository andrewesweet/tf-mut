package engine

// The population authority. The gate table's Full row is a claim a run has to
// earn, and until this value existed the earning was re-derived from the
// report at each call site: curate checked observation in one file, the
// baseline writer recomputed the row's shape in another, and the until-dry
// loop — whose dry claim is the strongest conclusion the tool draws — checked
// nothing at all. Here the claim is a value. A consumer that draws a
// conclusion over a population receives one that earned it, the population
// and its proof travel together, and the only route to either value is a
// constructor that refuses everything else.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// authoritativePopulation is a classified population every member of which
// was observed: no mutant timed out and none failed to evaluate. Both leave
// mutants unobserved, and an unobserved mutant is not an absent one — an
// empty kill set drawn over mutants that never ran is a false finding of
// exactly the kind this value exists to make unconstructible.
//
// The zero value proves nothing and no consumer accepts it: the value exists
// only where newAuthoritativePopulation declined to refuse.
type authoritativePopulation struct {
	// classified is the report the proof was made from: the classified
	// population the proof covers.
	classified report.Report
}

// freshPopulation refines an authoritative population with freshness: no
// verdict in it was replayed from the cache. It is the strictly narrower
// proof the baseline writer demands — staleness and a rewrite are claims
// about what actually ran — and it exists only on top of a proof of
// observation, never instead of one.
//
// The zero value proves nothing and no consumer accepts it: the value exists
// only where newFreshPopulation declined to refuse.
type freshPopulation struct {
	// proven is the observation proof the freshness claim refines.
	proven authoritativePopulation
}

// newAuthoritativePopulation is the constructor: the observation check curate
// once performed at its single call site, promoted to the one route to the
// value.
//
// The rule itself — which populations count as fully observed, and why — is
// stated once, here. refused is the sentinel of the rule the caller serves and
// rationale is that command's own remedy: the reasons are command-neutral, the
// refusal is spelled in the refusing command's terms.
func newAuthoritativePopulation(
	result report.Report,
	refused error,
	rationale string,
) (authoritativePopulation, error) {
	reasons := []string{}

	if timeouts := result.Count(report.Timeout); timeouts > 0 {
		reasons = append(reasons,
			strconv.Itoa(timeouts)+" mutant(s) timed out, so their kills were never observed")
	}

	if len(result.Errors) > 0 {
		reasons = append(reasons,
			strconv.Itoa(len(result.Errors))+" mutant(s) could not be evaluated at all")
	}

	if len(reasons) != 0 {
		return authoritativePopulation{}, fmt.Errorf("%w: %s\n%s",
			refused, strings.Join(reasons, "; "), rationale)
	}

	return authoritativePopulation{classified: result}, nil
}

// newFreshPopulation refines a proven population with freshness: it refuses
// anything that replayed even one verdict from the cache, with the cache
// row's own remedy.
func newFreshPopulation(proven authoritativePopulation) (freshPopulation, error) {
	if cached := proven.classified.Population.Cached; cached > 0 {
		return freshPopulation{}, fmt.Errorf("%w: this run replayed %d cached verdict(s); use --no-cache",
			ErrBaselineWrite, cached)
	}

	return freshPopulation{proven: proven}, nil
}

// graded is the classified population the proof covers.
func (a authoritativePopulation) graded() report.Report { return a.classified }

// graded is the classified population the freshness proof covers.
func (f freshPopulation) graded() report.Report { return f.proven.classified }
