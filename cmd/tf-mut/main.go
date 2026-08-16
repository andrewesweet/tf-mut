// Package main exposes the tf-mut command-line entry point.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/andrewesweet/tf-mut/internal/buildinfo"
)

var version = "dev"

const (
	versionCommand = "version"
	versionFlag    = "--version"
	usage          = "usage: tf-mut version"

	versionArgumentCount = 1
	firstArgument        = 0
	exitSuccess          = 0
	exitFailure          = 1
	exitUsage            = 2
)

func main() {
	os.Exit(run(os.Args[1:], version, os.Stdout, os.Stderr))
}

func run(args []string, buildVersion string, stdout, stderr io.Writer) int {
	if len(args) == versionArgumentCount &&
		(args[firstArgument] == versionCommand || args[firstArgument] == versionFlag) {
		_, err := fmt.Fprintln(stdout, buildinfo.Resolve(buildVersion))
		if err != nil {
			return exitFailure
		}

		return exitSuccess
	}

	_, err := fmt.Fprintln(stderr, usage)
	if err != nil {
		return exitFailure
	}

	return exitUsage
}
