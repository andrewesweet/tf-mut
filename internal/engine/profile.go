package engine

// The command's own CPU profile: started for a run when the request names a
// destination, stopped when the run finishes, and never written anywhere but
// the file the user asked for.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
)

var errCPUProfileInModule = errors.New("CPU profile destination is inside the module directory")

const cpuProfileFileMode = 0o600

func startCPUProfile(moduleDir, destination string, warnings io.Writer) (func(), error) {
	profilePath, profileErr := cpuProfilePath(moduleDir, destination)
	if profileErr != nil {
		return nil, profileErr
	}

	//nolint:gosec // caller-selected diagnostic file; O_EXCL refuses existing files and final symlinks.
	profileFile, profileErr := os.OpenFile(profilePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, cpuProfileFileMode)
	if profileErr != nil {
		return nil, fmt.Errorf("opening the CPU profile file: %w", profileErr)
	}

	writer := &cpuProfileWriter{Writer: profileFile}
	if startErr := pprof.StartCPUProfile(writer); startErr != nil {
		_ = profileFile.Close()
		return nil, fmt.Errorf("starting the CPU profile: %w", startErr)
	}

	return func() {
		pprof.StopCPUProfile()
		if profileErr := errors.Join(writer.err, profileFile.Close()); profileErr != nil && warnings != nil {
			_, _ = fmt.Fprintf(warnings, "tf-mut: CPU profile may be incomplete: %s\n",
				strings.ReplaceAll(profileErr.Error(), "\n", "; "))
		}
	}, nil
}

func cpuProfilePath(moduleDir, destination string) (string, error) {
	module, err := filepath.Abs(moduleDir)
	if err != nil {
		return "", fmt.Errorf("resolving module directory for the CPU profile: %w", err)
	}

	module, err = filepath.EvalSymlinks(module)
	if err != nil {
		return "", fmt.Errorf("resolving module directory for the CPU profile: %w", err)
	}

	path, err := filepath.Abs(destination)
	if err != nil {
		return "", fmt.Errorf("resolving the CPU profile destination: %w", err)
	}

	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("resolving the CPU profile parent directory: %w", err)
	}

	path = filepath.Join(parent, filepath.Base(path))

	if within(module, path) {
		return "", fmt.Errorf("%w: %q", errCPUProfileInModule, destination)
	}

	return path, nil
}

type cpuProfileWriter struct {
	io.Writer

	err error
}

// Write forwards the profile bytes to the wrapped writer and remembers the
// first error, so the caller can report it once after the run completes.
func (w *cpuProfileWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}

	if w.err == nil {
		w.err = err
	}

	return n, err
}
