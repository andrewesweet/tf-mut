package report_test

import (
	"context"
	"encoding/json"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewesweet/tf-mut/internal/report"
)

// The HTML contract is offline per-file mutant inspection. Chrome must render
// both grouped survivors' operators and literal source, rejecting a blank viewer
// or executable source data even when the generated page contains those strings.
func TestHTMLViewerRendersInBrowser(t *testing.T) {
	t.Parallel()

	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Skip("google-chrome is required for the HTML consumer regression")
	}

	directory := t.TempDir()
	value := indeterminateReport()
	value.ClosureRoot = directory
	// Module source is data: closing tags must remain literal and never run.
	const source = `# </script><script>window.sourceExecuted = true</script>`
	if err := os.WriteFile(filepath.Join(directory, "main.tf"),
		[]byte(strings.Repeat("\n", 20)+source+"\n\n# second mutant\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rendered := strings.Builder{}
	if err := report.WriteHTML(&rendered, value); err != nil {
		t.Fatalf("rendering: %v", err)
	}

	// Inspect the rendered shadow DOM, not the bundle's implementation text.
	const errors = `<script>window.viewerErrors = [];
window.addEventListener('error', event => window.viewerErrors.push(event.message));</script>`
	const probe = `<script>
window.addEventListener('load', () => {
  const elements = root => [...root.querySelectorAll('*')].flatMap(element =>
    [element, ...(element.shadowRoot ? elements(element.shadowRoot) : [])]);
  const text = root => [...root.childNodes].map(node =>
    node.nodeType === Node.TEXT_NODE ? node.textContent :
    node.nodeName === 'STYLE' || node.nodeName === 'SCRIPT' ? '' :
    text(node.shadowRoot || node)).join('');
  setTimeout(() => {
    const app = document.querySelector('mutation-test-report-app');
    const link = app.shadowRoot && elements(app.shadowRoot).find(element =>
      element.tagName === 'A' && element.textContent.trim() === 'main.tf');
    if (link) link.click();
    setTimeout(() => {
      const result = document.createElement('pre');
      result.id = 'viewer-result';
      result.textContent = JSON.stringify({
        registered: !!customElements.get('mutation-test-report-app'),
        errors: window.viewerErrors,
        fileOpened: !!link,
        sourceExecuted: !!window.sourceExecuted,
        rendered: app.shadowRoot ? text(app.shadowRoot) : ''
      });
      document.body.append(result);
    }, 1000);
  }, 1000);
});
</script>`
	path := filepath.Join(directory, "report.html")
	page := strings.Replace(rendered.String(), "<head>", "<head>"+errors, 1) + probe
	if err := os.WriteFile(path, []byte(page), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, browser,
		"--headless", "--disable-gpu", "--disable-background-networking",
		"--no-first-run", "--no-default-browser-check",
		"--user-data-dir="+filepath.Join(directory, "profile"),
		"--dump-dom", "--virtual-time-budget=3000", "file://"+path)
	var stderr strings.Builder
	command.Stderr = &stderr

	output, err := command.Output()
	if err != nil {
		t.Fatalf("opening HTML report: %v\n%s", err, stderr.String())
	}

	start := strings.Index(string(output), `<pre id="viewer-result">`)
	if start < 0 {
		t.Fatal("browser did not run the viewer probe")
	}

	content, _, _ := strings.Cut(string(output[start+len(`<pre id="viewer-result">`):]), "</pre>")
	var result struct {
		Registered     bool
		Errors         []string
		FileOpened     bool
		SourceExecuted bool
		Rendered       string
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(content)), &result); err != nil {
		t.Fatalf("decoding browser result: %v", err)
	}
	if !result.Registered || len(result.Errors) != 0 {
		t.Fatalf("viewer failed to register: %+v", result)
	}
	if !result.FileOpened {
		t.Fatal("viewer did not render a main.tf navigation link")
	}
	if result.SourceExecuted {
		t.Fatal("embedded module source executed as JavaScript")
	}

	for _, want := range []string{"NUM-OFF-BY-ONE", "BOOL-FLIP", source} {
		if !strings.Contains(result.Rendered, want) {
			t.Fatalf("per-file viewer did not render %q:\n%s", want, result.Rendered)
		}
	}
}
