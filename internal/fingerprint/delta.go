package fingerprint

import (
	"slices"
	"strings"
)

// Change is one observable difference between the baseline and a mutant.
type Change struct {
	// Run identifies the run block the difference appeared in.
	Run string
	// Path is the canonical payload path.
	Path string
	// Address is the Terraform address a reader would recognise, when the path
	// names one.
	Address string
	// Baseline and Mutant are the canonical renderings, with "" for absent.
	Baseline string
	Mutant   string
}

// Delta is the masked observable difference a mutant produced.
//
// An empty delta is the oracle's "fingerprint identical": every selected run
// produced, after masking, the same document it produced unmutated.
type Delta struct {
	// Changes lists the differences in canonical path order.
	Changes []Change
	// Indeterminate marks a comparison the mask could not make soundly. An
	// indeterminate delta is never identical, whatever its length.
	Indeterminate bool
	// MissingRuns names run blocks the mutant produced no payload for.
	MissingRuns []string
}

// Empty reports fingerprint identity: no masked difference and nothing
// undecidable about the comparison.
func (d Delta) Empty() bool {
	return len(d.Changes) == 0 && !d.Indeterminate && len(d.MissingRuns) == 0
}

// Addresses lists the distinct Terraform addresses the delta touches.
func (d Delta) Addresses() []string {
	seen := map[string]bool{}
	addresses := []string{}

	for _, change := range d.Changes {
		if change.Address == "" || seen[change.Address] {
			continue
		}

		seen[change.Address] = true

		addresses = append(addresses, change.Address)
	}

	slices.Sort(addresses)

	return addresses
}

// Compare computes the masked delta between a baseline and a mutant run set.
func Compare(mask Mask, baseline, mutant []Payload) Delta {
	delta := Delta{Changes: []Change{}, Indeterminate: false, MissingRuns: []string{}}

	byKey := map[string]Payload{}

	for _, payload := range mutant {
		masked, indeterminate := mask.Apply(payload)
		if indeterminate {
			delta.Indeterminate = true
		}

		byKey[payload.Key()] = masked
	}

	for _, payload := range baseline {
		maskedBaseline, indeterminate := mask.Apply(payload)
		if indeterminate {
			delta.Indeterminate = true
		}

		maskedMutant, found := byKey[payload.Key()]
		if !found {
			delta.MissingRuns = append(delta.MissingRuns, payload.Key())

			continue
		}

		delta.Changes = append(delta.Changes, comparePayload(payload.Key(), maskedBaseline, maskedMutant)...)

		delete(byKey, payload.Key())
	}

	for key := range byKey {
		delta.MissingRuns = append(delta.MissingRuns, key)
	}

	slices.Sort(delta.MissingRuns)
	slices.SortFunc(delta.Changes, func(left, right Change) int {
		if left.Run != right.Run {
			return strings.Compare(left.Run, right.Run)
		}

		return strings.Compare(left.Path, right.Path)
	})

	return delta
}

func comparePayload(key string, baseline, mutant Payload) []Change {
	changes := []Change{}

	for path, value := range baseline.Values {
		other, found := mutant.Values[path]
		if found && other == value {
			continue
		}

		changes = append(changes, Change{
			Run:      key,
			Path:     path,
			Address:  Address(path),
			Baseline: value,
			Mutant:   other,
		})
	}

	for path, value := range mutant.Values {
		if _, found := baseline.Values[path]; found {
			continue
		}

		changes = append(changes, Change{
			Run:      key,
			Path:     path,
			Address:  Address(path),
			Baseline: "",
			Mutant:   value,
		})
	}

	return changes
}

// Unknowns lists every unknown address across a run set, which is the evidence
// the conservative unknown rule carries: `Unobservable` may only be assigned
// when this is empty.
func Unknowns(payloads []Payload) []string {
	seen := map[string]bool{}
	addresses := []string{}

	for _, payload := range payloads {
		for _, address := range payload.Unknowns {
			if seen[address] {
				continue
			}

			seen[address] = true

			addresses = append(addresses, address)
		}
	}

	slices.Sort(addresses)

	return addresses
}
