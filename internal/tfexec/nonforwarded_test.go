//nolint:testpackage // the strip contract is asserted at the seam that owns it.
package tfexec

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// Commit 2 of the child-environment key (D3): session bookkeeping variables
// are stripped at this seam, so Terraform never receives them and the key —
// the same computation — never hashes them. A stripped variable cannot cause
// false reuse, because nothing that produces a verdict can read it.

// strippedNames is every exact name the seam withholds from the child. The
// M3 C4 addendum (25 October 2026) carries one line of evidence per name;
// this list must match nonForwardedVariables exactly, and the test below
// holds that.
var strippedNames = []string{
	// Git location variables (pre-existing).
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_COMMON_DIR",
	"GIT_NAMESPACE",
	"GIT_PREFIX",
	// Trace-context propagation.
	"TRACEPARENT",
	"TRACESTATE",
	// Shell bookkeeping.
	"SHLVL",
	"OLDPWD",
	"_",
	// Multiplexer and terminal identity.
	"TMUX",
	"TMUX_PANE",
	"TERM_PROGRAM",
	"TERM_PROGRAM_VERSION",
	// systemd per-invocation identifiers.
	"INVOCATION_ID",
	"SYSTEMD_EXEC_PID",
	"MEMORY_PRESSURE_WATCH",
	"MEMORY_PRESSURE_WRITE",
	"JOURNAL_STREAM",
	// SSH session identifiers.
	"SSH_CLIENT",
	"SSH_CONNECTION",
	"SSH_TTY",
	"XDG_SESSION_ID",
}

func TestTheStripSetMatchesTheReviewedList(t *testing.T) {
	t.Parallel()

	for _, name := range strippedNames {
		if !nonForwardedVariables[name] {
			t.Fatalf("reviewed %s is forwarded; the table drifted from the addendum", name)
		}
	}

	if len(nonForwardedVariables) != len(strippedNames) {
		t.Fatalf("the seam strips %d names against %d reviewed",
			len(nonForwardedVariables), len(strippedNames))
	}
}

func TestNonForwardedVariablesNeverReachTheChild(t *testing.T) {
	for _, name := range strippedNames {
		unsetVariable(t, name)
	}

	for _, name := range strippedNames {
		t.Setenv(name, "session-value")
	}

	// TF_MUT_DEBUG is how a debugging session reaches tf-mut: the debug flag
	// must not cost the cache, and tf-mut's whole namespace goes with it.
	t.Setenv("TF_MUT_DEBUG", "1")

	// The seam's own stripping step withholds every stripped name, including
	// the shell-owned ones the stub below would set itself.
	if stripped := stripNonForwarded(os.Environ()); slices.ContainsFunc(stripped, func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return nonForwardedVariables[name] || strings.HasPrefix(name, nonForwardedPrefix)
	}) {
		t.Fatal("the stripping step forwards a non-forwarded variable")
	}

	// The stub is a bash script, and bash sets SHLVL, _ and PWD on startup
	// however it was invoked, so those three are asserted above, not here.
	bashOwned := map[string]bool{"SHLVL": true, "_": true, "PWD": true}

	environment := childEnvironmentOf(t, environmentDumpingRunner(t))

	for _, name := range strippedNames {
		if bashOwned[name] {
			continue
		}

		if values, found := environment[name]; found {
			t.Fatalf("stripped %s reaches the child as %q", name, values)
		}
	}

	if values, found := environment["TF_MUT_DEBUG"]; found {
		t.Fatalf("stripped TF_MUT_DEBUG reaches the child as %q", values)
	}
}

// TestForwardedVariablesStillReachTheChild pins the other half of the seam:
// everything Terraform, its providers or git can read stays forwarded, so
// keying the child still misses where it must.
func TestForwardedVariablesStillReachTheChild(t *testing.T) {
	keep := map[string]string{
		"PATH":                        "/usr/bin:/bin",
		"HOME":                        "/home/dev",
		"TERM":                        "xterm-256color",
		"COLORTERM":                   "truecolor",
		"SSH_AUTH_SOCK":               "/tmp/ssh-agent.sock",
		"TF_VAR_names":                `["alpha","beta"]`,
		"TF_CLI_ARGS_test":            "-parallelism=1",
		"TF_CLI_CONFIG_FILE":          "/home/dev/.terraformrc",
		"TF_PLUGIN_CACHE_DIR":         "/tmp/plugin-cache",
		"TF_INPUT":                    "false",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
		"GIT_SSH_COMMAND":             "ssh -i /home/dev/.ssh/id",
		"GIT_CONFIG_COUNT":            "1",
	}

	for name, value := range keep {
		t.Setenv(name, value)
	}

	environment := childEnvironmentOf(t, environmentDumpingRunner(t))

	for name, want := range keep {
		assertSingleEntry(t, environment, name, want)
	}

	// PWD survives stripping — only the three shell-owned names are asserted
	// at the pure level, and PWD is the forwarded one of them — but a bash
	// stub reports its own working directory however it was invoked.
	if stripped := stripNonForwarded([]string{"PWD=/home/dev/tf-mut"}); !slices.Equal(stripped, []string{"PWD=/home/dev/tf-mut"}) {
		t.Fatalf("stripping withholds PWD: %q", stripped)
	}
}

// TestRunAdditionsAreNeverStripped: stripping applies to the inherited
// environment only. A run's own additions are explicit per-run input — the
// provisioner tests address the child through TF_MUT_MARKER — and they enter
// the key beside the child, so the two still agree.
func TestRunAdditionsAreNeverStripped(t *testing.T) {
	runner := environmentDumpingRunner(t)
	runner.Env = []string{
		"TF_MUT_MARKER=/tmp/marker",
		"TRACEPARENT=addition-value",
	}

	environment := childEnvironmentOf(t, runner)

	assertSingleEntry(t, environment, "TF_MUT_MARKER", "/tmp/marker")
	assertSingleEntry(t, environment, "TRACEPARENT", "addition-value")
}

// replayTransition is one recorded environment transition: the session kinds
// on each side, the names that changed between them, and what the
// child-environment key must decide. The inputs come from the design review's
// transition tables (d-series over the all-killed fixture, U-series over
// utl-regions); the D3 column is the expected decision.
type replayTransition struct {
	name string
	// mutate applies the recorded diff to a copy of the base environment.
	mutate func(env map[string]string)
	// wantHit is the D3 column: true where only stripped names differ.
	wantHit bool
}

// replayBase is a representative session environment carrying every stripped
// name and the forwarded names the MISS rows turn on.
func replayBase() map[string]string {
	return map[string]string{
		"PATH":                        "/usr/bin:/bin",
		"HOME":                        "/home/dev",
		"TERM":                        "xterm-256color",
		"COLORTERM":                   "truecolor",
		"PWD":                         "/home/dev/tf-mut",
		"SHELL":                       "/bin/bash",
		"SHLVL":                       "1",
		"OLDPWD":                      "/home/dev",
		"_":                           "/usr/bin/bash",
		"TRACEPARENT":                 "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0b902b7-01",
		"TRACESTATE":                  "rojo=00f067aa0b902b7",
		"TMUX":                        "/tmp/tmux-1000/default,1234,0",
		"TMUX_PANE":                   "%0",
		"TERM_PROGRAM":                "vscode",
		"TERM_PROGRAM_VERSION":        "1.2",
		"INVOCATION_ID":               "11111111111111111111111111111111",
		"SYSTEMD_EXEC_PID":            "100",
		"MEMORY_PRESSURE_WATCH":       "/sys/fs/cgroup/user.slice/memory.pressure",
		"MEMORY_PRESSURE_WRITE":       "/sys/fs/cgroup/user.slice/cgroup.pressure",
		"JOURNAL_STREAM":              "8:100",
		"SSH_CLIENT":                  "10.0.0.1 54321 22",
		"SSH_CONNECTION":              "10.0.0.1 54321 10.0.0.2 22",
		"SSH_TTY":                     "/dev/pts/0",
		"XDG_SESSION_ID":              "c3",
		"SSH_AUTH_SOCK":               "/tmp/ssh-agent.1000",
		"GIT_CONFIG_COUNT":            "1",
		"GIT_CONFIG_KEY_0":            "user.email",
		"GIT_CONFIG_VALUE_0":          "dev@example.com",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
		"TF_VAR_names":                `["alpha","beta"]`,
	}
}

func replayTransitions() []replayTransition {
	return []replayTransition{
		{
			name: "agent to agent (d1-d2): TRACEPARENT only",
			mutate: func(env map[string]string) {
				env["TRACEPARENT"] = "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"
			},
			wantHit: true,
		},
		{
			name: "agent to tmux (d2-d3): multiplexer plus PATH",
			mutate: func(env map[string]string) {
				env["TMUX"] = "/tmp/tmux-1000/tmux,200,0"
				env["TMUX_PANE"] = "%3"
				env["PATH"] = "/usr/local/bin:/usr/bin:/bin"
				env["SHLVL"] = "2"
				env["TERM"] = "tmux-256color"
				env["TERM_PROGRAM"] = "tmux"
				env["TERM_PROGRAM_VERSION"] = "3.6a"
				env["TRACEPARENT"] = "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"
			},
			wantHit: false,
		},
		{
			name: "tmux to systemd (d3-d4): OTLP endpoint plus wholesale session change",
			mutate: func(env map[string]string) {
				env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://collector:4318"
				env["PATH"] = "/usr/bin:/bin:/snap/bin"
				env["SSH_AUTH_SOCK"] = "/run/user/1000/keyring/ssh"
				env["INVOCATION_ID"] = "22222222222222222222222222222222"
				env["SYSTEMD_EXEC_PID"] = "200"
				delete(env, "TMUX")
				delete(env, "TMUX_PANE")
				delete(env, "SSH_CLIENT")
				delete(env, "SSH_CONNECTION")
				delete(env, "SSH_TTY")
			},
			wantHit: false,
		},
		{
			name: "systemd to systemd (d4-1-d4-2): per-invocation identifiers only",
			mutate: func(env map[string]string) {
				env["INVOCATION_ID"] = "33333333333333333333333333333333"
				env["MEMORY_PRESSURE_WATCH"] = "/sys/fs/cgroup/system.slice/memory.pressure"
				env["SYSTEMD_EXEC_PID"] = "300"
			},
			wantHit: true,
		},
		{
			name: "systemd to clean login (d4-2-d5): PATH and session removal",
			mutate: func(env map[string]string) {
				env["TERM"] = "dumb"
				env["OLDPWD"] = "/"
				env["PATH"] = "/usr/bin:/bin"
				delete(env, "SSH_AUTH_SOCK")
				delete(env, "GIT_CONFIG_COUNT")
				delete(env, "GIT_CONFIG_KEY_0")
				delete(env, "GIT_CONFIG_VALUE_0")
				delete(env, "OTEL_EXPORTER_OTLP_ENDPOINT")
				delete(env, "INVOCATION_ID")
			},
			wantHit: false,
		},
		{
			name: "clean login to agent (d5-d6): wholesale session replacement",
			mutate: func(env map[string]string) {
				env["PATH"] = "/home/dev/.local/bin:/usr/bin:/bin"
				env["SSH_AUTH_SOCK"] = "/tmp/ssh-agent.9999"
				env["TRACEPARENT"] = "00-cccccccccccccccccccccccccccccccc-dddddddddddddddd-01"
				env["TERM"] = "xterm-256color"
				env["XDG_SESSION_ID"] = "c7"
				env["GIT_CONFIG_COUNT"] = "2"
			},
			wantHit: false,
		},
		{
			name: "agent to agent with debug (U1-U2): TF_MUT_DEBUG plus TRACEPARENT",
			mutate: func(env map[string]string) {
				env["TF_MUT_DEBUG"] = "1"
				env["TRACEPARENT"] = "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"
			},
			wantHit: true,
		},
		{
			name: "agent to agent (U3-U4): OLDPWD plus TRACEPARENT",
			mutate: func(env map[string]string) {
				env["OLDPWD"] = "/home/dev/tf-mut/docs"
				env["TRACEPARENT"] = "00-cccccccccccccccccccccccccccccccc-dddddddddddddddd-01"
			},
			wantHit: true,
		},
		{
			name: "agent to systemd login (U4-U5): OTLP endpoint plus wholesale session change",
			mutate: func(env map[string]string) {
				env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://collector:4318"
				env["PATH"] = "/usr/bin:/bin:/snap/bin"
				env["SSH_AUTH_SOCK"] = "/run/user/1000/keyring/ssh"
				delete(env, "TRACEPARENT")
				delete(env, "SSH_CLIENT")
				delete(env, "SSH_CONNECTION")
				delete(env, "SSH_TTY")
				delete(env, "TMUX")
				delete(env, "TMUX_PANE")
			},
			wantHit: false,
		},
	}
}

// TestRecordedTransitionsReplayAgainstTheChildEnvironmentKey replays every
// environment transition the design review recorded — the six fixture
// transitions and the three suite transitions — against the real derivation.
// A row passes when the key agrees with the review's D3 column: HIT exactly
// where only stripped names differ. The false-reuse half of the acceptance
// (a HIT whose fresh verdicts differ) is proved at the engine seam, where
// real verdicts exist to compare.
func TestRecordedTransitionsReplayAgainstTheChildEnvironmentKey(t *testing.T) {
	t.Parallel()

	for _, transition := range replayTransitions() {
		t.Run(transition.name, func(t *testing.T) {
			t.Parallel()

			before := replayBase()
			after := replayBase()
			transition.mutate(after)

			beforeChild := childEnvironment(stripNonForwarded(flatten(before)), nil)
			afterChild := childEnvironment(stripNonForwarded(flatten(after)), nil)

			slices.Sort(beforeChild)
			slices.Sort(afterChild)

			hit := slices.Equal(beforeChild, afterChild)
			if hit != transition.wantHit {
				t.Fatalf("transition %q replays as %v, want hit=%v",
					transition.name, hitString(hit), transition.wantHit)
			}
		})
	}
}

func flatten(env map[string]string) []string {
	entries := make([]string, 0, len(env))

	for name, value := range env {
		entries = append(entries, name+"="+value)
	}

	return entries
}

func hitString(hit bool) string {
	if hit {
		return "HIT"
	}

	return "MISS"
}
