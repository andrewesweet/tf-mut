package discovery

import (
	"slices"

	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// ProviderAlias is one declared provider configuration.
type ProviderAlias struct {
	// Name is the provider local name.
	Name string
	// Alias is the configuration alias, empty for the default configuration.
	Alias string
}

// collectProviderBlock records a `provider` declaration and its alias.
func collectProviderBlock(module *Module, block *hclsyntax.Block) {
	if len(block.Labels) != 1 {
		return
	}

	declared := ProviderAlias{Name: block.Labels[0], Alias: ""}

	if attribute, found := block.Body.Attributes["alias"]; found {
		declared.Alias = literalString(attribute.Expr)
	}

	module.ProviderAliases = append(module.ProviderAliases, declared)
}

// ProviderAliases lists the aliases declared for a provider across the closure,
// in declaration-independent order.
func (c Configuration) ProviderAliases(name string) []string {
	aliases := []string{}

	for _, module := range c.Modules {
		for _, declared := range module.ProviderAliases {
			if declared.Name != name || declared.Alias == "" || slices.Contains(aliases, declared.Alias) {
				continue
			}

			aliases = append(aliases, declared.Alias)
		}
	}

	slices.Sort(aliases)

	return aliases
}

// AliasMocked reports whether a provider configuration is covered by a
// `mock_provider` block.
//
// A `mock_provider` with no alias covers the default configuration only;
// Terraform matches mocks to configurations by alias, so the tool must too, or
// the PROVIDER-ALIAS-SWAP guard would compare the wrong thing.
func (c Configuration) AliasMocked(name, alias string) bool {
	for _, mocked := range c.Tests.Mocks {
		if mocked.Name == name && mocked.Alias == alias {
			return true
		}
	}

	return false
}
