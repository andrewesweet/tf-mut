package buildchain_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestEnsureToolsChecksSourceBuildIdentity(t *testing.T) {
	t.Parallel()

	for binary, module := range map[string]string{
		"gopls":       "golang.org/x/tools/gopls",
		"goimports":   "golang.org/x/tools",
		"govulncheck": "golang.org/x/vuln",
	} {
		for _, mismatch := range []string{
			"none", "compiler", "main-version", "dependency-version",
			"dependency-checksum", "added-replacement", "removed-replacement",
			"replacement-version", "replacement-path", "replacement-checksum", "same-replacement",
		} {
			t.Run(binary+"/"+mismatch, func(t *testing.T) {
				t.Parallel()

				root := sourceToolsFixture(t)
				mainModule := debug.Module{Path: module, Version: "v1.2.3", Sum: "h1:main"}
				dependency := debug.Module{Path: "example.com/dependency", Version: "v1.0.0", Sum: "h1:dependency"}
				info := debug.BuildInfo{GoVersion: runtime.Version(), Main: mainModule, Deps: []*debug.Module{&dependency}}
				pinnedDependency := dependency
				if strings.Contains(mismatch, "replacement") && mismatch != "added-replacement" {
					pinnedDependency.Replace = &debug.Module{Path: "example.com/replacement", Version: "v1.1.0", Sum: "h1:replacement"}
					replacement := *pinnedDependency.Replace
					dependency.Replace = &replacement
				}
				switch mismatch {
				case "compiler":
					info.GoVersion = "go1.26.6"
				case "main-version":
					info.Main.Version = "v1.2.2"
				case "dependency-version":
					dependency.Version = "v0.9.0"
				case "dependency-checksum":
					dependency.Sum = "h1:obsolete"
				case "added-replacement":
					dependency.Replace = &debug.Module{Path: "example.com/other", Version: "v1.0.0", Sum: "h1:other"}
				case "removed-replacement":
					dependency.Replace = nil
				case "replacement-version":
					dependency.Replace.Version = "v1.0.0"
				case "replacement-path":
					dependency.Replace.Path = "example.com/other"
				case "replacement-checksum":
					dependency.Replace.Sum = "h1:obsolete"
				}
				writeSourceToolJSON(t, root, binary+"-modules.json", mainModule, pinnedDependency)
				writeSourceToolJSON(t, root, binary+"-build.json", info)
				command := exec.CommandContext(t.Context(), "bash", filepath.Join(repositoryRoot(t), "scripts/ensure-tools"))
				command.Dir = root
				command.Env = append(os.Environ(), "TEST_ROOT="+root, "TEST_COMPILER="+runtime.Version(),
					"PATH="+filepath.Join(root, "mock-bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("ensure source tools: %v\n%s", err, output)
				}
				_, err := os.Stat(filepath.Join(root, "rebuilt"))
				wantRebuild := mismatch != "none" && mismatch != "same-replacement"
				if wantRebuild && err != nil {
					t.Fatalf("stale %s did not invoke the installer: %v", binary, err)
				}
				if !wantRebuild && !os.IsNotExist(err) {
					t.Fatalf("current %s invoked the installer: %v", binary, err)
				}
				if _, err := os.Stat(filepath.Join(root, "download")); !os.IsNotExist(err) {
					t.Fatalf("freshness check requested uncached, unused module metadata: %v", err)
				}
			})
		}
	}
}

func sourceToolsFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	command := exec.CommandContext(t.Context(), "git", "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize tool fixture: %v\n%s", err, output)
	}
	writeFixtureFile(t, root, "mock-bin/mise", []byte(`#!/usr/bin/env bash
set -euo pipefail
if [[ $1 == install ]]; then
  exit 0
fi
shift 2
if [[ $1 == scripts/install-source-tools ]]; then
  touch "$TEST_ROOT/rebuilt"
elif [[ $1 == go ]]; then
  case "$2" in
    env) printf '%s\n' "$TEST_COMPILER" ;;
    list)
      shift 2
      while [[ $1 == -* ]]; do shift; done
      for module in "$@"; do
        if [[ $module == all || $module == *...* ]]; then
          touch "$TEST_ROOT/download"
          exit 1
        fi
        jq --arg module "$module" 'select(.Path == $module)' "$TEST_ROOT/${PWD##*/}-modules.json"
      done
      ;;
    version)
      binary=${!#}
      cat "$TEST_ROOT/${binary##*/}-build.json"
      ;;
    *) exit 2 ;;
  esac
else
  exec "$@"
fi
`))
	for binary, module := range map[string]string{
		"gopls": "golang.org/x/tools/gopls", "goimports": "golang.org/x/tools", "govulncheck": "golang.org/x/vuln",
	} {
		writeFixtureFile(t, root, ".tools/bin/"+binary, nil)
		if err := os.Chmod(filepath.Join(root, ".tools/bin", binary), testDirectoryMode); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, "tools", binary), testDirectoryMode); err != nil {
			t.Fatal(err)
		}
		mainModule := debug.Module{Path: module, Version: "v1.2.3", Sum: "h1:main"}
		dependency := debug.Module{Path: "example.com/dependency", Version: "v1.0.0", Sum: "h1:dependency"}
		writeSourceToolJSON(t, root, binary+"-modules.json", mainModule, dependency)
		writeSourceToolJSON(t, root, binary+"-build.json", debug.BuildInfo{
			GoVersion: runtime.Version(), Main: mainModule, Deps: []*debug.Module{&dependency},
		})
	}
	if err := os.Chmod(filepath.Join(root, "mock-bin/mise"), testDirectoryMode); err != nil {
		t.Fatal(err)
	}

	return root
}

func writeSourceToolJSON(t *testing.T, root, name string, values ...any) {
	t.Helper()

	var contents bytes.Buffer
	encoder := json.NewEncoder(&contents)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
	}
	writeFixtureFile(t, root, name, contents.Bytes())
}
