package engine_test

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewesweet/tf-mut/internal/engine"
	"github.com/andrewesweet/tf-mut/internal/report"
	"github.com/andrewesweet/tf-mut/internal/testsupport"
)

// registryFixture is a loopback module registry together with the certificate
// Terraform must trust it by. Everything it serves is local: the module
// payload it hands out is a git repository on the same filesystem, built by
// the same helper the remote-module tests use, so a versioned registry module
// can be installed — and its installed version recorded — without reaching
// the network.
type registryFixture struct {
	// host is the registry source host, port included, as a module source
	// spells it: 127.0.0.1:port.
	host string
	// certFile is the PEM bundle that makes the loopback TLS certificate
	// trusted, passed to Terraform as SSL_CERT_FILE.
	certFile string
}

// serveRegistry serves the three module-registry endpoints Terraform needs —
// service discovery, one version, one download — over TLS, backing the
// download with the given git origin.
func serveRegistry(t *testing.T, origin string) registryFixture {
	t.Helper()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimRight(r.URL.Path, "/") {
		case "/.well-known/terraform.json":
			body := `{"modules.v1":"https://` + r.Host + `/v1/modules/"}`
			serveJSON(t, w, body)
		case "/v1/modules/test/local/null/versions":
			body := `{"modules":[{"versions":[{"version":"0.0.1","root":` +
				`{"submodules":[],"providers":[{"name":"null","source":"hashicorp/null"}]}}]}]}`
			serveJSON(t, w, body)
		case "/v1/modules/test/local/null/0.0.1/download":
			// The download location is the X-Terraform-Get header, and the
			// double slash names the subdirectory inside the git origin.
			w.Header().Set("X-Terraform-Get", "git::file://"+origin+"//mod")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	certFile := filepath.Join(t.TempDir(), "registry-ca.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(certFile, encoded, 0o600); err != nil {
		t.Fatalf("writing loopback registry certificate: %v", err)
	}

	return registryFixture{
		host:     strings.TrimPrefix(server.URL, "https://"),
		certFile: certFile,
	}
}

func serveJSON(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	//nolint:gosec // a loopback test registry serving JSON to Terraform's own client.
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("serving registry response: %v", err)
	}
}

// versionedConsumer builds a root module whose child is a versioned registry
// module: the shape whose installed version Terraform records in the module
// manifest and re-checks on every later command.
func versionedConsumer(t *testing.T, fixture registryFixture) string {
	t.Helper()

	module := filepath.Join(t.TempDir(), "consumer")
	if err := os.MkdirAll(filepath.Join(module, "tests"), 0o750); err != nil {
		t.Fatalf("creating consumer: %v", err)
	}

	testsupport.WriteFile(t, filepath.Join(module, "main.tf"), `module "local" {
  source  = "`+fixture.host+`/test/local/null"
  version = "0.0.1"

  prefix = "root"
}

output "name" {
  value = module.local.name
}
`)

	testsupport.WriteFile(t, filepath.Join(module, "tests", "unit.tftest.hcl"), `run "wires_the_versioned_module" {
  command = plan

  assert {
    condition     = output.name == "root-module"
    error_message = "the versioned module must resolve inside the sandbox"
  }
}
`)

	return module
}

// TestAMutantSandboxKeepsTheInstalledModuleVersion: the module manifest a
// sandbox synthesises must keep each module's installed Version. Terraform
// re-checks the recorded version against the configuration's constraint on
// every command; a manifest that silently drops it makes Terraform refuse
// every mutant with "Module version requirements have changed", so a suite
// whose modules call versioned registry modules reports every mutant Invalid
// instead of scoring any.
//
// The registry is loopback TLS backed by a local git origin, so the versioned
// install — the only kind that records a version — happens offline.
func TestAMutantSandboxKeepsTheInstalledModuleVersion(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required to build the remote-module fixture")
	}

	origin := gitOrigin(t)
	registry := serveRegistry(t, origin)
	module := versionedConsumer(t, registry)

	config := baseConfig(t, module)
	// SSL_CERT_FILE is how a Go client — Terraform among them — is pointed at
	// a private root of trust. Scoped to this run's Terraform environment, it
	// makes the loopback registry's certificate trusted for the install.
	config.Env = append(config.Env, "SSL_CERT_FILE="+registry.certFile)

	result, err := engine.Run(t.Context(), config)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(result.Mutants) == 0 {
		t.Fatal("no mutants were generated for the versioned-module consumer")
	}

	for _, mutant := range result.Mutants {
		summary := ""
		if len(mutant.Diagnostics) > 0 {
			summary = mutant.Diagnostics[0].Summary
		}
		if mutant.State == report.Invalid {
			t.Fatalf("mutant %s at %s was Invalid (%q); the sandbox dropped the "+
				"module manifest's Version and Terraform refused the mutant",
				mutant.ID, mutant.Site, summary)
		}
	}

	if got := stateOf(t, result, "output.name"); got != report.Killed {
		t.Fatalf("the versioned module's output = %s, want %s; the module must be "+
			"present and scored, not refused", got, report.Killed)
	}
}
