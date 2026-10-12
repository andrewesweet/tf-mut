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
)

var (
	errCommandUsage   = errors.New("usage: buildchain verify-providers|sync-go-toolchains")
	errUnknownCommand = errors.New("unknown build-chain command")
)

func main() {
	err := run(context.Background(), os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(failureExitCode)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) != commandArgumentCount {
		return errCommandUsage
	}
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}

	switch args[commandArgumentIndex] {
	case "verify-providers":
		err = buildchain.VerifyProviderChain(root)
	case "sync-go-toolchains":
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			<-ctx.Done()
			stop()
		}()

		err = buildchain.SyncGoToolchains(ctx, root, runtime.Version())
	default:
		return fmt.Errorf("%w %q", errUnknownCommand, args[commandArgumentIndex])
	}
	if err != nil {
		return fmt.Errorf("run %s: %w", args[commandArgumentIndex], err)
	}

	return nil
}
