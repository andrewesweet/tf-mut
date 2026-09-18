package mutation

import (
	"embed"
	"fmt"
	"slices"
	"strings"
)

// A shipped pack is embedded in the binary under its reserved name (M5c.2)
// and parsed by the same `ParsePack` contract a user pack goes through, so
// one contract governs both. A shipped pack that fails its own contract is an
// init-time failure, never a silent skip: the pack is data the binary
// carries, and a binary whose shipped data cannot load must not build.

//go:embed packs/*.hcl
var shippedPackFiles embed.FS

// shippedPackDirectory is the embedded directory the pack files live in. The
// file stem is the pack's reserved name.
const shippedPackDirectory = "packs"

// shippedPacks holds every embedded pack, parsed once at init.
//
//nolint:gochecknoglobals // the embedded pack set is immutable by construction.
var shippedPacks = mustParseShippedPacks()

// mustParseShippedPacks parses every embedded pack file through the user
// pack's own contract. A failure panics: a shipped pack that cannot load is
// a broken build, not a run-time surprise.
func mustParseShippedPacks() map[string]Pack {
	entries, err := shippedPackFiles.ReadDir(shippedPackDirectory)
	if err != nil {
		panic(fmt.Sprintf("the shipped packs directory cannot be read: %v", err))
	}

	packs := make(map[string]Pack, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".hcl") {
			panic(fmt.Sprintf("the shipped packs directory holds non-pack entry %q", entry.Name()))
		}

		name := strings.TrimSuffix(entry.Name(), ".hcl")

		content, err := shippedPackFiles.ReadFile(fsPath(shippedPackDirectory, entry.Name()))
		if err != nil {
			panic(fmt.Sprintf("the shipped pack %s cannot be read: %v", name, err))
		}

		pack, err := ParsePack(name, fsPath(shippedPackDirectory, entry.Name()), content)
		if err != nil {
			panic(fmt.Sprintf("the shipped pack %s fails its own contract: %v", name, err))
		}

		pack.Embedded = true
		packs[name] = pack
	}

	return packs
}

// fsPath joins an embed.FS path with forward slashes, whatever the host
// separator is.
func fsPath(directory, name string) string {
	return directory + "/" + name
}

// ShippedPack returns the embedded pack registered under the given reserved
// name, and whether one exists.
func ShippedPack(name string) (Pack, bool) {
	pack, found := shippedPacks[name]

	return pack, found
}

// reservedPackNames are the shipped packs' names, derived from the embedded
// files: a pack reserves its name in the change that ships it, never ahead
// of it, so a user pack registered today cannot be shadowed by a shipped
// pack tomorrow.
//
//nolint:gochecknoglobals // an immutable, init-derived list.
var reservedPackNames = shippedPackNames()

func shippedPackNames() []string {
	names := make([]string, 0, len(shippedPacks))
	for name := range shippedPacks {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// Every shipped entry must state its upstream provenance: `source_rule` and
// `source_licence` are optional for a user pack and required for a shipped
// one, so the check runs here where the shipped packs are, once at init. A
// shipped pack that violates its contract must fail the process at start-up,
// not at first use: an embedded pack is code, and code that cannot keep its
// own contract is a broken build.
func init() { //nolint:gochecknoinits // a broken shipped pack is a build failure, not a recoverable state.
	for _, name := range reservedPackNames {
		for _, entry := range shippedPacks[name].Entries {
			if entry.SourceRule == "" || entry.SourceLicence == "" {
				panic(fmt.Sprintf("the shipped pack %s: entry %q does not state its source rule and licence",
					name, entry.ID))
			}
		}
	}
}
