package engine

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tfconfig "github.com/andrewesweet/tf-mut/internal/config"
	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// The pack registration and selection contract (M5c.1). Registration is a
// `pack "NAME" { file = "PATH" }` block in `.tf-mut.hcl`, the path resolved
// relative to the module root; selection is by name, from the `--pack` flag
// and the configured `operators { packs }` list merged as a union and
// deduplicated by name. Every refusal here is a configuration error decided
// before any Terraform runs: an unknown name, a user pack shadowing a
// reserved shipped name, and a pack file that fails the contract.

// selectedPacks merges the flag and configured lists into one name list.
func selectedPacks(flagged, configured []string) []string {
	names := append(slices.Clone(flagged), configured...)
	slices.Sort(names)

	return slices.Compact(names)
}

// resolvePacks checks the registrations and loads every selected pack.
func resolvePacks(moduleDir string, selected []string, registered []tfconfig.Pack) ([]mutation.Pack, error) {
	reserved := mutation.ReservedPackNames()

	for _, registration := range registered {
		if slices.Contains(reserved, registration.Name) {
			return nil, fmt.Errorf("%w: pack %q shadows the reserved name of a shipped pack; "+
				"the reserved names are %s", tfconfig.ErrConfig, registration.Name,
				strings.Join(reserved, ", "))
		}
	}

	packs := make([]mutation.Pack, 0, len(selected))

	for _, name := range selected {
		index := slices.IndexFunc(registered, func(registration tfconfig.Pack) bool {
			return registration.Name == name
		})
		if index < 0 {
			return nil, fmt.Errorf("%w: pack %q is not registered: no pack block in %s declares it "+
				"and no shipped pack has that name", tfconfig.ErrConfig, name, tfconfig.FileName)
		}

		path := registered[index].File
		if !filepath.IsAbs(path) {
			path = filepath.Join(moduleDir, path)
		}

		pack, err := mutation.LoadPack(name, path)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", tfconfig.ErrConfig, err)
		}

		packs = append(packs, pack)
	}

	return packs, nil
}
