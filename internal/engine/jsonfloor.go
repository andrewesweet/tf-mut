package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/andrewesweet/tf-mut/internal/discovery"
)

// The two safety gates, named so that the floor can say which one an unread
// file's class could have informed.
const (
	// FlagAllowRealInfrastructure is the opt-in that authorises execution
	// against a provider no `mock_provider` block covers.
	FlagAllowRealInfrastructure = "--allow-real-infrastructure"
	// FlagAllowUnsandboxedEffects is the opt-in that authorises apply-mode
	// execution of the constructs mocking does not sever.
	FlagAllowUnsandboxedEffects = "--allow-unsandboxed-effects"
)

// jsonFloor is the milestone's entry gate: what an unread JSON file in the
// closure costs the run.
//
// Terraform reads `.tf.json`, `.tftest.json` and the JSON variables classes at
// execution time, because a sandbox copies the whole closure. Anything the tool
// has not decoded is therefore content Terraform will act on and the tool is
// blind to — so while any of it is unread, every claim that depended on having
// read the whole configuration is withdrawn, and each safety gate the unread
// content could have informed fails closed on its own flag.
type jsonFloor struct {
	// unread is the closure's undecoded JSON inventory.
	unread []discovery.JSONFile
}

// floorOf computes the floor for a configuration.
func floorOf(configuration discovery.Configuration) jsonFloor {
	return jsonFloor{unread: configuration.UnreadJSON()}
}

// active reports whether the floor is down: any unread JSON at all.
//
// The static consequences — no static shortcuts, every adapter mapping treated
// as failed — hold for every class, including the variables class, because a
// variables file the tool cannot read changes what a static evaluation would
// have concluded.
func (f jsonFloor) active() bool {
	return len(f.unread) > 0
}

// informing lists the unread files whose class could have carried the risk the
// named gate authorises.
//
// The mapping is by what the grammar can express, not by what any particular
// file happens to contain — the whole point is that the content is unknown:
//
//   - `.tf.json` can declare `required_providers`, a resource of any provider,
//     a provisioner, and the data sources mocking does not sever, so it can
//     carry either risk.
//   - `.tftest.json` can declare a `mock_provider` block — so its absence from
//     the mock inventory makes an unmocked provider look mocked — and an
//     apply-mode run, which is what makes an effect execute. Either risk.
//   - the JSON variables classes declare values. They cannot declare a
//     provider, a provisioner or a run, so they inform neither gate; they
//     inform the static evaluation, which the floor's other half covers.
func (f jsonFloor) informing(flag string) []discovery.JSONFile {
	informing := []discovery.JSONFile{}

	for _, file := range f.unread {
		if slices.Contains(gatesInformedBy(file.Class), flag) {
			informing = append(informing, file)
		}
	}

	return informing
}

func gatesInformedBy(class discovery.JSONClass) []string {
	switch class {
	case discovery.JSONConfiguration, discovery.JSONTest:
		return []string{FlagAllowRealInfrastructure, FlagAllowUnsandboxedEffects}
	case discovery.JSONVariables:
		return nil
	default:
		// An unrecognised class is content of unknown shape: it fails closed on
		// both gates rather than on neither.
		return []string{FlagAllowRealInfrastructure, FlagAllowUnsandboxedEffects}
	}
}

// checkFloor refuses the run unless every gate the unread content could have
// informed has been authorised, and returns the warnings for the gates that
// were.
//
// The two gates are checked independently: authorising one never lifts the
// other, because they authorise different risks.
func (f jsonFloor) checkFloor(config Config) ([]string, error) {
	warnings := []string{}

	gates := []struct {
		flag    string
		allowed bool
		err     error
		risk    string
	}{
		{
			flag:    FlagAllowRealInfrastructure,
			allowed: config.AllowRealInfrastructure,
			err:     ErrRealInfrastructure,
			risk: "could declare a provider no mock_provider block covers, or a mock this run " +
				"cannot see, so the provider and mock inventories are unknowable",
		},
		{
			flag:    FlagAllowUnsandboxedEffects,
			allowed: config.AllowUnsandboxedEffects,
			err:     ErrUnsandboxedEffects,
			risk: "could declare a provisioner, an unsevered data source, or the apply-mode run " +
				"that executes one, so the effect inventory is unknowable",
		},
	}

	for _, gate := range gates {
		informing := f.informing(gate.flag)
		if len(informing) == 0 {
			continue
		}

		if !gate.allowed {
			return nil, fmt.Errorf("%w: %s\n"+
				"  Unread JSON %s.\n"+
				"  Pass %s to proceed anyway",
				gate.err, describeUnread(informing), gate.risk, gate.flag)
		}

		warnings = append(warnings, fmt.Sprintf(
			"unread JSON in the closure (%s): %s was accepted for content the tool has not read",
			strings.Join(relativeNames(informing), ", "), gate.flag,
		))
	}

	return warnings, nil
}

// degradation is the warning the floor's static half always carries: the run
// keeps going, with every shortcut and every adapter mapping withdrawn.
func (f jsonFloor) degradation() string {
	return fmt.Sprintf(
		"unread JSON in the closure (%s): static shortcuts are disabled and every address "+
			"mapping is treated as failed, so the whole-payload rule is the floor for every mutant",
		strings.Join(relativeNames(f.unread), ", "),
	)
}

func describeUnread(files []discovery.JSONFile) string {
	lines := make([]string, 0, len(files))

	for _, file := range files {
		lines = append(lines, fmt.Sprintf("\n  %s (%s file): %s",
			file.Rel, file.Class, file.Reason))
	}

	return strings.Join(lines, "")
}

func relativeNames(files []discovery.JSONFile) []string {
	names := make([]string, 0, len(files))

	for _, file := range files {
		names = append(names, file.Rel)
	}

	return names
}
