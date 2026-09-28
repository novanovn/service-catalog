package api

import (
	"os"
	"testing"
)

// TestParseLambdaFunctionsAgainstRealDemoRepo runs the parser against the actual
// package.json produced by the neuron-claude-ai-standard-boilerplate template,
// rather than an inline fixture — so template drift is caught here.
//
// Skips when the demo repo is absent (CI, another machine), because this is a
// local integration fixture, not a checked-in dependency.
func TestParseLambdaFunctionsAgainstRealDemoRepo(t *testing.T) {
	const demoPkg = "/Users/novanhariman/Documents/Ngulik/repo/demo/" +
		"lmd-oona-ph-integration-demo-quote-svc/package.json"

	data, err := os.ReadFile(demoPkg)
	if err != nil {
		t.Skipf("demo repo not present on this machine: %v", err)
	}

	got := ParseLambdaFunctionsFromPackageJSON(data)
	if len(got) != 2 {
		t.Fatalf("expected 2 functions from the demo repo, got %d", len(got))
	}

	want := map[string]string{
		"demo-quote-svc-calculator-v1-svc": "dist/index.demoQuoteCalculatorHandler",
		"demo-quote-svc-fetcher-v1-hdr":    "dist/index.demoQuoteFetcherHandler",
	}

	for _, ref := range got {
		wantHandler, ok := want[ref.Name]
		if !ok {
			t.Errorf("unexpected function name %q", ref.Name)
			continue
		}
		if ref.Handler != wantHandler {
			t.Errorf("function %q: handler = %q, want %q", ref.Name, ref.Handler, wantHandler)
		}
	}

	// The core gap this work closes: not one of the real Lambda names equals the
	// repo name, which is what Pre-Flight currently derives its single target from.
	const repoName = "lmd-oona-ph-integration-demo-quote-svc"
	for _, ref := range got {
		if ref.Name == repoName {
			t.Errorf("unexpected: function %q equals the repo name; "+
				"the multi-function gap assumption no longer holds", ref.Name)
		}
	}
}
