// Package buildchain implements internal checks and update helpers for the shared build chain.
package buildchain

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const (
	requiredProviderAddress  = "registry.terraform.io/hashicorp/null"
	requiredProviderPlatform = "linux_amd64"
	providerAddressField     = 0
	providerVersionField     = 1
	providerPlatformField    = 2
	providerPinFieldCount    = 3
	providerArchiveCount     = 1
	hclQuote                 = `"`
)

var (
	errFixtureProviderMismatch = errors.New("fixture-b provider constraint does not match allowlist")
	errProviderLockMismatch    = errors.New("provider lock does not match allowlist")
	errProviderArchiveAuth     = errors.New("provider lock does not authenticate mirror archive")
	errProviderMirrorMismatch  = errors.New("provider mirror does not contain exactly the allowed archive")
	errCLIConfigMismatch       = errors.New("terraform CLI configuration does not match provider allowlist")
	errAllowlistEntryCount     = errors.New("provider allowlist must contain exactly one three-field entry")
	errUnsupportedProviderPin  = errors.New("unsupported provider allowlist entry")
	errUnstableGoVersion       = errors.New("go runtime version is not a stable semantic version")
)

// VerifyProviderChain proves that the fixture constraint, lock hash, allowlist, mirror, and
// generated Terraform CLI configuration describe the same provider package.
func VerifyProviderChain(root string) error {
	repository := os.DirFS(root)
	pin, err := readProviderPin(repository)
	if err != nil {
		return fmt.Errorf("read provider pin: %w", err)
	}
	checks := []func() error{
		func() error { return verifyFixtureProvider(repository, pin) },
		func() error { return verifyProviderLock(repository, pin) },
		func() error { return verifyProviderMirror(repository, pin) },
		func() error { return verifyTerraformCLIConfig(repository, root, pin) },
	}
	for _, check := range checks {
		err = check()
		if err != nil {
			return err
		}
	}

	return nil
}

func verifyFixtureProvider(repository fs.FS, pin providerPin) error {
	fixture, err := readFile(repository, "research/spikes/fixture-b/main.tf")
	if err != nil {
		return err
	}
	shortAddress := strings.TrimPrefix(pin.address, "registry.terraform.io/")
	if !strings.Contains(fixture, "source = "+hclQuote+shortAddress+hclQuote) ||
		!strings.Contains(fixture, "version = "+hclQuote+pin.version+hclQuote) {
		return fmt.Errorf("%w: %s %s", errFixtureProviderMismatch, pin.address, pin.version)
	}

	return nil
}

func verifyProviderLock(repository fs.FS, pin providerPin) error {
	lock, err := readFile(repository, "research/spikes/fixture-b/.terraform.lock.hcl")
	if err != nil {
		return err
	}
	for _, required := range []string{
		"provider " + hclQuote + pin.address + hclQuote,
		"version     = " + hclQuote + pin.version + hclQuote,
		"constraints = " + hclQuote + pin.version + hclQuote,
	} {
		if !strings.Contains(lock, required) {
			return fmt.Errorf("%w: missing %q", errProviderLockMismatch, required)
		}
	}

	providerArchive := providerArchivePath(pin)
	archive, err := fs.ReadFile(repository, providerArchive)
	if err != nil {
		return fmt.Errorf("read provider archive %s: %w", providerArchive, err)
	}
	archiveHash := fmt.Sprintf("zh:%x", sha256.Sum256(archive))
	if !strings.Contains(lock, archiveHash) {
		return fmt.Errorf("%w: %s", errProviderArchiveAuth, archiveHash)
	}

	return nil
}

func verifyProviderMirror(repository fs.FS, pin providerPin) error {
	providerArchive := providerArchivePath(pin)
	archives, err := fs.Glob(repository, ".tools/providers/*/*/*/terraform-provider-*.zip")
	if err != nil {
		return fmt.Errorf("discover provider archives: %w", err)
	}
	if len(archives) != providerArchiveCount || archives[providerAddressField] != providerArchive {
		return fmt.Errorf("%w: got %v, want [%s]", errProviderMirrorMismatch, archives, providerArchive)
	}

	return nil
}

func verifyTerraformCLIConfig(repository fs.FS, root string, pin providerPin) error {
	cliConfig, err := readFile(repository, ".tools/terraform-cli.tfrc")
	if err != nil {
		return err
	}
	for _, required := range []string{
		filepath.Join(root, ".tools/providers"),
		filepath.Join(root, ".tools/terraform-plugin-cache"),
		pin.address,
	} {
		if !strings.Contains(cliConfig, required) {
			return fmt.Errorf("%w: missing %q", errCLIConfigMismatch, required)
		}
	}

	return nil
}

func providerArchivePath(pin providerPin) string {
	return path.Join(
		".tools/providers",
		pin.address,
		fmt.Sprintf("terraform-provider-null_%s_%s.zip", pin.version, pin.platform),
	)
}

type providerPin struct {
	address  string
	version  string
	platform string
}

func readProviderPin(repository fs.FS) (providerPin, error) {
	contents, err := readFile(repository, "tools/providers.allowlist")
	if err != nil {
		return providerPin{}, err
	}
	fields := strings.Fields(contents)
	if len(fields) != providerPinFieldCount {
		return providerPin{}, errAllowlistEntryCount
	}
	pin := providerPin{
		address:  fields[providerAddressField],
		version:  fields[providerVersionField],
		platform: fields[providerPlatformField],
	}
	if pin.address != requiredProviderAddress || pin.platform != requiredProviderPlatform {
		return providerPin{}, fmt.Errorf("%w: %s", errUnsupportedProviderPin, strings.Join(fields, " "))
	}

	return pin, nil
}

func readFile(repository fs.FS, name string) (string, error) {
	contents, err := fs.ReadFile(repository, name)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}

	return string(contents), nil
}

// ToolchainVersions contains a module language floor and exact toolchain preference.
type ToolchainVersions struct {
	Language string
	Exact    string
}

// GoToolchainVersions derives toolchain versions from a stable Go runtime such as go1.26.6.
func GoToolchainVersions(goVersion string) (ToolchainVersions, error) {
	var major, minor, patchVersion int
	parsed, err := fmt.Sscanf(goVersion, "go%d.%d.%d", &major, &minor, &patchVersion)
	if err != nil || parsed != providerPinFieldCount ||
		fmt.Sprintf("go%d.%d.%d", major, minor, patchVersion) != goVersion {
		return ToolchainVersions{}, fmt.Errorf("%w: %q", errUnstableGoVersion, goVersion)
	}

	return ToolchainVersions{Language: fmt.Sprintf("%d.%d.0", major, minor), Exact: goVersion}, nil
}

// SyncGoToolchains updates every repository Go module to the active stable Go toolchain.
func SyncGoToolchains(ctx context.Context, root, goVersion string) error {
	versions, err := GoToolchainVersions(goVersion)
	if err != nil {
		return fmt.Errorf("derive Go toolchain versions: %w", err)
	}

	for _, module := range []string{".", "tools/gopls", "tools/goimports", "tools/govulncheck"} {
		// The executable and arguments are repository-controlled; only a validated semantic
		// version is interpolated into the go command.
		command := exec.CommandContext( //nolint:gosec // See the repository-controlled invariant above.
			ctx,
			"go",
			"mod",
			"edit",
			"-go="+versions.Language,
			"-toolchain="+versions.Exact,
		)
		command.Dir = filepath.Join(root, module)
		output, commandErr := command.CombinedOutput()
		if commandErr != nil {
			return fmt.Errorf("sync Go toolchain in %s: %w: %s", module, commandErr, output)
		}
	}

	return nil
}
