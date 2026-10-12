package engine

// The operator catalogue as the reporters render it.

import (
	"github.com/andrewesweet/tf-mut/internal/mutation"
	"github.com/andrewesweet/tf-mut/internal/report"
)

// RuleDescriptions renders the operator catalogue for the reporters, so that a
// SARIF rule carries the catalogue's own words rather than a paraphrase.
func RuleDescriptions() map[string]report.RuleDescription {
	descriptions := make(map[string]report.RuleDescription, len(mutation.Catalogue()))

	for id, entry := range mutation.Descriptions() {
		descriptions[id] = report.RuleDescription{
			Tier:        string(entry.Tier),
			Description: entry.Description,
			Killer:      entry.Killer,
		}
	}

	return descriptions
}
