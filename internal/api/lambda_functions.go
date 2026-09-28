package api

import (
	"encoding/json"
	"sort"
	"strings"
)

// LambdaFunctionRef is one deployed AWS Lambda declared in a service repo's
// package.json `functions` manifest.
//
// The neuron-claude-ai-standard-boilerplate template writes this manifest into
// every scaffolded service. For a multi-function repo the manifest is the only
// place that maps the repo to the real Lambda names Terraform will create —
// those names are NOT derivable from the repo name. Example, for repo
// lmd-oona-ph-integration-demo-quote-svc:
//
//	demo-quote-svc-calculator-v1-svc   (handler dist/index.demoQuoteCalculatorHandler)
//	demo-quote-svc-fetcher-v1-hdr      (handler dist/index.demoQuoteFetcherHandler)
type LambdaFunctionRef struct {
	// Key is the manifest key, e.g. "calculator-svc". Identifier only.
	Key string `json:"key"`
	// Name is the real AWS Lambda function name devops' Terraform must create.
	Name string `json:"name"`
	// Handler is the built entry point, e.g. "dist/index.demoQuoteCalculatorHandler".
	Handler string `json:"handler"`
}

// ParseLambdaFunctionsFromPackageJSON extracts the `functions` manifest from a
// service repo's package.json.
//
// It returns nil for any input it cannot use — malformed JSON, a missing or
// empty `functions` field, or a `functions` value that is not an object. Callers
// MUST treat nil as "unknown, fall back to the legacy single-name behaviour"
// rather than as an error: a repo whose manifest cannot be read must never be
// blocked from approval on that basis alone.
//
// Individual unusable entries (no name, blank name) are skipped rather than
// discarding the whole manifest, so one bad entry cannot hide its valid siblings.
//
// Results are sorted by Name because Go randomises map iteration order, and an
// unstable order would make the Pre-Flight UI reshuffle between reloads.
func ParseLambdaFunctionsFromPackageJSON(data []byte) []LambdaFunctionRef {
	if len(data) == 0 {
		return nil
	}

	var pkg struct {
		Functions map[string]struct {
			Name    string `json:"name"`
			Handler string `json:"handler"`
		} `json:"functions"`
	}
	// A non-object `functions` value (array, string, number) fails here and is
	// treated the same as a missing manifest.
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}

	refs := make([]LambdaFunctionRef, 0, len(pkg.Functions))
	for key, fn := range pkg.Functions {
		name := strings.TrimSpace(fn.Name)
		if name == "" {
			// Without a deployable name there is nothing to verify against AWS.
			continue
		}
		refs = append(refs, LambdaFunctionRef{
			Key:     strings.TrimSpace(key),
			Name:    name,
			Handler: strings.TrimSpace(fn.Handler),
		})
	}

	if len(refs) == 0 {
		return nil
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs
}
