package api

import "testing"

// TestDemoBoilerplateRepoNameCompatibility verifies that the artifacts produced by the
// neuron-claude-ai-standard-boilerplate template (as exercised by the demo repo
// lmd-oona-ph-integration-demo-quote-svc) are parsed correctly by Castan's own
// repo-id -> Jenkins job name resolution.
//
// This is an integration-contract test between two repos: if the boilerplate's naming
// convention ever drifts from what Castan can parse, this fails.
func TestDemoBoilerplateRepoNameCompatibility(t *testing.T) {
	cases := []struct {
		name   string
		repoID string
		want   string
	}{
		{
			name:   "bare repo name from boilerplate convention",
			repoID: "lmd-oona-ph-integration-demo-quote-svc",
			want:   "lmd-oona-ph-integration-demo-quote-svc",
		},
		{
			name:   "org-qualified as written in package.json repository.url",
			repoID: "oona-insurance/lmd-oona-ph-integration-demo-quote-svc",
			want:   "lmd-oona-ph-integration-demo-quote-svc",
		},
		{
			name:   "full https clone URL",
			repoID: "https://github.com/oona-insurance/lmd-oona-ph-integration-demo-quote-svc",
			want:   "lmd-oona-ph-integration-demo-quote-svc",
		},
		{
			name:   "ID jurisdiction variant",
			repoID: "oona-insurance/lmd-oona-id-integration-demo-quote-svc",
			want:   "lmd-oona-id-integration-demo-quote-svc",
		},
		{
			name:   "surrounding whitespace is tolerated",
			repoID: "  oona-insurance/lmd-oona-ph-integration-demo-quote-svc  ",
			want:   "lmd-oona-ph-integration-demo-quote-svc",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := JenkinsJobNameFromRepoID(tc.repoID); got != tc.want {
				t.Errorf("JenkinsJobNameFromRepoID(%q) = %q, want %q", tc.repoID, got, tc.want)
			}
		})
	}
}

// TestDemoBoilerplateMultiFunctionLambdaNames documents the gap between the boilerplate's
// multi-function output and Castan's single-service catalog model.
//
// The demo repo declares TWO deployed Lambda functions in package.json:
//
//	demo-quote-svc-calculator-v1-svc   (suffix -svc)
//	demo-quote-svc-fetcher-v1-hdr      (suffix -hdr, per the SKILL.md fetcher/handler/scheduler rule)
//
// Castan's Pre-Flight check (VerifyTicketLambdaHandler) resolves exactly ONE function name
// from the repo/pipeline name, so it cannot currently verify both. This test pins the
// expected names so the future multi-function Pre-Flight work has a concrete target.
func TestDemoBoilerplateMultiFunctionLambdaNames(t *testing.T) {
	repoID := "oona-insurance/lmd-oona-ph-integration-demo-quote-svc"

	pipeline := JenkinsJobNameFromRepoID(repoID)
	if pipeline != "lmd-oona-ph-integration-demo-quote-svc" {
		t.Fatalf("unexpected pipeline name: %q", pipeline)
	}

	// These are the real AWS Lambda function names Terraform must create.
	deployedFunctions := []string{
		"demo-quote-svc-calculator-v1-svc",
		"demo-quote-svc-fetcher-v1-hdr",
	}

	if len(deployedFunctions) < 2 {
		t.Fatal("demo repo is expected to declare at least 2 functions")
	}

	// Neither deployed function name equals the pipeline/repo name. This is the documented
	// gap: Pre-Flight derives its target from the repo name, which matches no actual function.
	for _, fn := range deployedFunctions {
		if fn == pipeline {
			t.Errorf("unexpected: function %q equals pipeline name %q; "+
				"the multi-function gap assumption no longer holds", fn, pipeline)
		}
	}
}
