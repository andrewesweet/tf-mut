// Command buildchain runs internal build-chain verification and update helpers.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/andrewesweet/tf-mut/internal/buildchain"
)

const (
	commandArgumentCount = 1
	commandArgumentIndex = 0
	failureExitCode      = 1
	releaseSkipExitCode  = 2
)

var (
	errCommandUsage   = errors.New("usage: buildchain verify-providers|sync-go-toolchains|next-patch-version")
	errUnknownCommand = errors.New("unknown build-chain command")
)

func main() {
	err := run(context.Background(), os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		code := failureExitCode
		if errors.Is(err, buildchain.ErrReleasePublished) || errors.Is(err, buildchain.ErrReleaseUsage) {
			code = releaseSkipExitCode
		}
		os.Exit(code)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errCommandUsage
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}

	if args[commandArgumentIndex] == "next-patch-version" {
		version, versionErr := buildchain.NextPatchVersion(ctx, root, args[1:])
		if versionErr != nil {
			return versionErr
		}
		_, err = fmt.Fprintln(os.Stdout, version)
		return err
	}
	if len(args) != commandArgumentCount {
		return errCommandUsage
	}

	switch args[commandArgumentIndex] {
	case "verify-providers":
		err = buildchain.VerifyProviderChain(root)
	case "sync-go-toolchains":
		signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			<-signalCtx.Done()
			stop()
		}()

		err = buildchain.SyncGoToolchains(signalCtx, root, runtime.Version())
	default:
		return fmt.Errorf("%w %q", errUnknownCommand, args[commandArgumentIndex])
	}
	if err != nil {
		return fmt.Errorf("run %s: %w", args[commandArgumentIndex], err)
	}

	return nil
}
