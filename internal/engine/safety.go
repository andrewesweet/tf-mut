package engine

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
)

// checkSafety applies both safety gates before anything executes.
//
// Both are decided statically from the parsed configuration, so a refusal is
// guaranteed to happen before Terraform has evaluated a single resource — which
// is the whole point of the provisioner gate.
func checkSafety(configuration discovery.Configuration, config Config) ([]string, error) {
	warnings := []string{}

	unmocked := configuration.UnmockedProviders()
	if len(unmocked) > 0 {
		estimate := estimateRuns(configuration)

		if !config.AllowRealInfrastructure {
			return nil, fmt.Errorf(
				"%w: provider %s is not mocked by any mock_provider block in %s.%s\n"+
					"  A mutation run would execute about %d test runs against it.\n"+
					"  Pass --allow-real-infrastructure to proceed anyway",
				ErrRealInfrastructure, strings.Join(unmocked, ", "),
				filepath.Base(configuration.Tests.Dir),
				describeProviderUsers(configuration, unmocked), estimate,
			)
		}

		warnings = append(warnings, fmt.Sprintf(
			"running against unmocked provider(s) %s: about %d test runs will execute",
			strings.Join(unmocked, ", "), estimate,
		))
	}

	effects := configuration.Effects()
	if len(effects) == 0 || !configuration.Tests.HasApplyRun() {
		return warnings, nil
	}

	if !config.AllowUnsandboxedEffects {
		return nil, fmt.Errorf("%w: %s\n"+
			"  Mocking does not sever these; an apply-mode run executes them once per mutant.\n"+
			"  Pass --allow-unsandboxed-effects to proceed anyway",
			ErrUnsandboxedEffects, describeEffects(configuration, effects))
	}

	warnings = append(warnings, fmt.Sprintf(
		"apply-mode runs will execute %d unsandboxed effect(s) once per mutant", len(effects),
	))

	return warnings, nil
}

// estimateRuns is the number of test runs a full mutation run would execute,
// reported before the user is asked to accept the risk.
func estimateRuns(configuration discovery.Configuration) int {
	sites := 0

	for _, module := range configuration.Modules {
		sites += len(module.Outputs) + len(module.Locals) + len(module.Resources) + len(module.DataSources)
	}

	return sites * len(configuration.Tests.Runs)
}

// describeProviderUsers names the blocks that would reach an unmocked
// provider, with their source ranges, so the refusal points at something the
// reader can go and change.
func describeProviderUsers(configuration discovery.Configuration, unmocked []string) string {
	wanted := map[string]bool{}
	for _, provider := range unmocked {
		wanted[provider] = true
	}

	lines := []string{}

	for _, module := range configuration.Modules {
		for _, block := range append(append([]discovery.Block{}, module.Resources...), module.DataSources...) {
			if !wanted[discovery.ProviderOf(block.Type)] {
				continue
			}

			lines = append(lines, fmt.Sprintf("\n  %s at %s:%d:%d",
				block.Address, relativeTo(configuration, block.File),
				block.DefRange.Start.Line, block.DefRange.Start.Column))
		}
	}

	return strings.Join(lines, "")
}

func relativeTo(configuration discovery.Configuration, path string) string {
	if relative, err := filepath.Rel(configuration.ClosureRoot, path); err == nil {
		return filepath.ToSlash(relative)
	}

	return path
}

func describeEffects(configuration discovery.Configuration, effects []discovery.Effect) string {
	lines := make([]string, 0, len(effects))

	for _, effect := range effects {
		lines = append(lines, fmt.Sprintf("\n  %s in %s at %s:%d:%d",
			effect.Kind, effect.Address, relativeTo(configuration, effect.File),
			effect.Range.Start.Line, effect.Range.Start.Column))
	}

	return strings.Join(lines, "")
}
