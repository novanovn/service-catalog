package api

import "testing"

// TestParseLambdaFunctionsFromPackageJSON covers the happy path: a multi-function
// service repo scaffolded by neuron-claude-ai-standard-boilerplate.
func TestParseLambdaFunctionsFromPackageJSON(t *testing.T) {
	multi := []byte(`{
	  "name": "lmd-oona-ph-integration-demo-quote-svc",
	  "functions": {
	    "calculator-svc": { "name": "demo-quote-svc-calculator-v1-svc", "handler": "dist/index.demoQuoteCalculatorHandler" },
	    "fetcher-svc":    { "name": "demo-quote-svc-fetcher-v1-hdr",   "handler": "dist/index.demoQuoteFetcherHandler" }
	  }
	}`)

	got := ParseLambdaFunctionsFromPackageJSON(multi)
	if len(got) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(got))
	}

	// Go map iteration is randomised — the parser must sort so the UI stays stable.
	if got[0].Name != "demo-quote-svc-calculator-v1-svc" {
		t.Errorf("expected sorted order, got %q first", got[0].Name)
	}
	if got[1].Name != "demo-quote-svc-fetcher-v1-hdr" {
		t.Errorf("expected fetcher second, got %q", got[1].Name)
	}
	if got[0].Key != "calculator-svc" {
		t.Errorf("key mismatch: got %q", got[0].Key)
	}
	if got[0].Handler != "dist/index.demoQuoteCalculatorHandler" {
		t.Errorf("handler mismatch: got %q", got[0].Handler)
	}
}

// TestParseLambdaFunctionsSingleFunction guards the far more common shape:
// the hundreds of existing single-function repos.
func TestParseLambdaFunctionsSingleFunction(t *testing.T) {
	single := []byte(`{
	  "name": "lmd-oona-ph-integration-health-renewal-svc",
	  "functions": {
	    "create-quote-svc": { "name": "health-renewal-create-quote-v1-svc", "handler": "dist/index.healthCreateQuoteHandler" }
	  }
	}`)

	got := ParseLambdaFunctionsFromPackageJSON(single)
	if len(got) != 1 {
		t.Fatalf("expected 1 function, got %d", len(got))
	}
	if got[0].Name != "health-renewal-create-quote-v1-svc" {
		t.Errorf("name mismatch: got %q", got[0].Name)
	}
}

// TestParseLambdaFunctionsDeterministicOrder proves ordering does not depend on
// Go's randomised map iteration — run repeatedly, the result must not change.
func TestParseLambdaFunctionsDeterministicOrder(t *testing.T) {
	data := []byte(`{
	  "functions": {
	    "zulu-svc":  { "name": "svc-zulu-v1-svc",  "handler": "dist/index.zuluHandler" },
	    "alpha-svc": { "name": "svc-alpha-v1-svc", "handler": "dist/index.alphaHandler" },
	    "mike-svc":  { "name": "svc-mike-v1-svc",  "handler": "dist/index.mikeHandler" }
	  }
	}`)

	want := []string{"svc-alpha-v1-svc", "svc-mike-v1-svc", "svc-zulu-v1-svc"}
	for i := 0; i < 50; i++ {
		got := ParseLambdaFunctionsFromPackageJSON(data)
		if len(got) != len(want) {
			t.Fatalf("iteration %d: expected %d functions, got %d", i, len(want), len(got))
		}
		for j, w := range want {
			if got[j].Name != w {
				t.Fatalf("iteration %d: position %d = %q, want %q", i, j, got[j].Name, w)
			}
		}
	}
}

// TestParseLambdaFunctionsEdgeCases: every unusable input must yield nil so the
// caller falls back to legacy single-name behaviour instead of blocking approval.
func TestParseLambdaFunctionsEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"no functions field", `{"name":"lmd-oona-ph-integration-x"}`},
		{"empty functions object", `{"functions":{}}`},
		{"malformed json", `{oops`},
		{"empty input", ``},
		{"entry without name", `{"functions":{"a":{"handler":"dist/index.h"}}}`},
		{"blank name", `{"functions":{"a":{"name":"   "}}}`},
		{"functions is an array not object", `{"functions":[{"name":"x"}]}`},
		{"functions is a string", `{"functions":"nope"}`},
		{"null functions", `{"functions":null}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseLambdaFunctionsFromPackageJSON([]byte(tc.in)); len(got) != 0 {
				t.Errorf("expected 0 functions for %s, got %d", tc.name, len(got))
			}
		})
	}
}

// TestParseLambdaFunctionsSkipsUnusableEntries: a partially-broken manifest should
// still yield the entries that ARE usable, rather than discarding everything.
func TestParseLambdaFunctionsSkipsUnusableEntries(t *testing.T) {
	mixed := []byte(`{
	  "functions": {
	    "good-svc": { "name": "svc-good-v1-svc", "handler": "dist/index.goodHandler" },
	    "bad-svc":  { "handler": "dist/index.badHandler" },
	    "blank-svc": { "name": "  " }
	  }
	}`)

	got := ParseLambdaFunctionsFromPackageJSON(mixed)
	if len(got) != 1 {
		t.Fatalf("expected only the usable entry, got %d", len(got))
	}
	if got[0].Name != "svc-good-v1-svc" {
		t.Errorf("name mismatch: got %q", got[0].Name)
	}
}

// TestParseLambdaFunctionsTrimsWhitespace: values are trimmed so a stray space in
// package.json cannot produce an AWS lookup for " name-with-space".
func TestParseLambdaFunctionsTrimsWhitespace(t *testing.T) {
	padded := []byte(`{
	  "functions": {
	    "  spaced-svc  ": { "name": "  svc-spaced-v1-svc  ", "handler": "  dist/index.spacedHandler  " }
	  }
	}`)

	got := ParseLambdaFunctionsFromPackageJSON(padded)
	if len(got) != 1 {
		t.Fatalf("expected 1 function, got %d", len(got))
	}
	if got[0].Key != "spaced-svc" {
		t.Errorf("key not trimmed: %q", got[0].Key)
	}
	if got[0].Name != "svc-spaced-v1-svc" {
		t.Errorf("name not trimmed: %q", got[0].Name)
	}
	if got[0].Handler != "dist/index.spacedHandler" {
		t.Errorf("handler not trimmed: %q", got[0].Handler)
	}
}
