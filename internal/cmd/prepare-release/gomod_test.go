package main

import (
	"strings"
	"testing"
)

func TestGoModRegexps(t *testing.T) {
	t.Run("module declaration", func(t *testing.T) {
		testRegexp(t, moduleDeclarationRE,
			[]string{ // accepted
				"module go.temporal.io/sdk",
				"module go.temporal.io/sdk/contrib/envconfig",
			},
			[]string{ // rejected
				"// module go.temporal.io/sdk",
				"modules go.temporal.io/sdk",
				"module",
			})
	})
	t.Run("Temporal module requirement", func(t *testing.T) {
		testRegexp(t, temporalModuleRequirementRE,
			[]string{ //accepted
				"require go.temporal.io/api v1.63.4",
				"\tgo.temporal.io/sdk v1.48.0",
				"require go.temporal.io/sdk/contrib/envconfig v0.2.1 // indirect",
			},
			[]string{ //rejected
				"module go.temporal.io/sdk",
				"replace go.temporal.io/sdk => ../../",
				"require example.com/dependency v1.0.0",
				"require go.temporal.io/api",
				"// require go.temporal.io/api v1.63.4",
			})
	})
	t.Run("tagged Go version", func(t *testing.T) {
		testRegexp(t, goVersionRE,
			[]string{ //accepted
				"v1.63.4",
				"v1.64.0",
				"v0.2.1",
			},
			[]string{ //rejected
				"1.63.4",
				"v1.63",
				"v1.64.0-rc.1",
				"v1.64.0+build.1",
				"v1.063.4",
				"v1.63.04",
				"v0.0.0-20260730213819-7f6a96199578",
				"v1.63.1-0.20260730213819-7f6a96199578",
			})
	})
}

func TestValidateGoMod(t *testing.T) {
	const modulePath = "go.temporal.io/sdk/contrib/tally"
	tests := []struct {
		name  string
		goMod string
		// wantErr is a substring of the expected error. An empty value means the
		// go.mod is valid.
		wantErr string
	}{
		{
			name: "released dependency",
			goMod: stripIndentation(`
				module go.temporal.io/sdk/contrib/tally

				require go.temporal.io/sdk v1.12.0
			`),
		},
		{
			name: "no Temporal dependency",
			goMod: stripIndentation(`
				module go.temporal.io/sdk/contrib/tally
			`),
		},
		{
			name: "non-Temporal pseudo-version",
			goMod: stripIndentation(`
				module go.temporal.io/sdk/contrib/tally

				require example.com/dependency v0.0.0-20260730213819-7f6a96199578
			`),
		},
		{
			name: "missing module declaration",
			goMod: stripIndentation(`
				require go.temporal.io/sdk v1.48.0
			`),
			wantErr: "could not find a module declaration",
		},
		{
			name: "other module",
			goMod: stripIndentation(`
				module go.temporal.io/sdk/contrib/envconfig

				require go.temporal.io/sdk v1.48.0
			`),
			wantErr: `expected "go.temporal.io/sdk/contrib/tally"`,
		},
		{
			name: "prerelease dependency",
			goMod: stripIndentation(`
				module go.temporal.io/sdk/contrib/tally

				require go.temporal.io/api v1.64.0-rc.1
			`),
			wantErr: "must use an official release",
		},
		{
			name: "pseudo-version dependency",
			goMod: stripIndentation(`
				module go.temporal.io/sdk/contrib/tally

				require go.temporal.io/api v1.63.1-0.20260730213819-7f6a96199578
			`),
			wantErr: "must use an official release",
		},
		{
			name: "commit pseudo-version dependency",
			goMod: stripIndentation(`
				module go.temporal.io/sdk/contrib/tally

				require go.temporal.io/api v0.0.0-20260730213819-7f6a96199578
			`),
			wantErr: "must use an official release",
		},
		{
			name: "unreleased SDK dependency",
			goMod: stripIndentation(`
				module go.temporal.io/sdk/contrib/tally

				require go.temporal.io/sdk v1.48.1-0.20260804123456-abcdef123456
			`),
			wantErr: "must use an official release",
		},
		{
			name: "unreleased sibling contrib module",
			goMod: stripIndentation(`
				module go.temporal.io/sdk/contrib/tally

				require (
					go.temporal.io/sdk v1.48.0
					go.temporal.io/sdk/contrib/gcp/gcsdriver v0.0.0-00010101000000-000000000000
				)
			`),
			wantErr: "must use an official release",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateGoMod(test.goMod, modulePath)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("expected go.mod to be valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("expected an error containing %q, got %v", test.wantErr, err)
			}
		})
	}
}
