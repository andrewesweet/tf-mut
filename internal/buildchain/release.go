package buildchain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

var (
	ErrReleasePublished      = errors.New("HEAD already has a published release")
	ErrReleaseUsage          = errors.New("usage: next-patch-version [--prev version]")
	errNoReleaseTag          = errors.New("no vMAJOR.MINOR.PATCH tag to succeed")
	errNoPreviousRelease     = errors.New("no release tag precedes version")
	errInvalidReleaseVersion = errors.New("not a vMAJOR.MINOR.PATCH version")
	releaseVersionPattern    = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
)

type releaseVersion struct {
	tag   string
	parts [3]big.Int
}

func NextPatchVersion(ctx context.Context, root string, args []string) (string, error) {
	if len(args) != 0 && (len(args) != 2 || args[0] != "--prev") {
		return "", ErrReleaseUsage
	}
	if len(args) == 2 && !releaseVersionPattern.MatchString(args[1]) {
		return "", fmt.Errorf("%w: %s", errInvalidReleaseVersion, args[1])
	}
	tags, err := releaseCommand(ctx, root, "git", "tag", "--list", "v*")
	if err != nil {
		return "", err
	}
	versions := parseReleaseVersions(tags)
	if len(args) == 2 {
		target := parseReleaseVersions(args[1])[0]
		for _, version := range versions {
			if compareReleaseVersions(version, target) < 0 {
				return version.successor(), nil
			}
		}
		return "", fmt.Errorf("%w: %s", errNoPreviousRelease, args[1])
	}
	if len(versions) == 0 {
		return "", errNoReleaseTag
	}
	headTags, err := releaseCommand(ctx, root, "git", "tag", "--points-at", "HEAD")
	if err != nil {
		return "", err
	}
	if existing := parseReleaseVersions(headTags); len(existing) > 0 {
		tag := existing[0].tag
		published, lookupErr := releaseCommand(ctx, root, "gh", "api", "--paginate",
			"repos/{owner}/{repo}/releases", "--jq", ".[] | select(.draft == false) | .tag_name")
		if lookupErr != nil {
			return "", lookupErr
		}
		if slices.Contains(strings.Fields(published), tag) {
			return "", fmt.Errorf("%w: %s", ErrReleasePublished, tag)
		}
		return tag, nil
	}
	return versions[0].successor(), nil
}

func parseReleaseVersions(tags string) []releaseVersion {
	var versions []releaseVersion
	for tag := range strings.FieldsSeq(tags) {
		if !releaseVersionPattern.MatchString(tag) {
			continue
		}
		version := releaseVersion{tag: tag}
		for index, part := range strings.Split(tag[1:], ".") {
			version.parts[index].SetString(part, 10)
		}
		versions = append(versions, version)
	}
	slices.SortFunc(versions, func(a, b releaseVersion) int {
		return compareReleaseVersions(b, a)
	})
	return versions
}

func compareReleaseVersions(a, b releaseVersion) int {
	for index := range a.parts {
		if order := a.parts[index].Cmp(&b.parts[index]); order != 0 {
			return order
		}
	}
	return 0
}

func (version releaseVersion) successor() string {
	patch := new(big.Int).Add(&version.parts[2], big.NewInt(1))
	return fmt.Sprintf("v%s.%s.%s", &version.parts[0], &version.parts[1], patch)
}

func releaseCommand(ctx context.Context, root, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}
