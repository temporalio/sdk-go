package main

import (
	"strings"
	"testing"
)

func TestVersionRegexps(t *testing.T) {
	t.Run("version", func(t *testing.T) {
		testRegexp(t, versionRE,
			// accepted:
			[]string{"1.48.0", "0.2.1", "2.0.0"},
			// rejected:
			[]string{"v1.48.0", "1.48", "1.048.0", "1.48.00", "1.48.0-rc.1", "1.48.0+build.1", "1.48.0 release"})
	})
	t.Run("SDK version declaration", func(t *testing.T) {
		testRegexp(t, sdkVersionRE,
			// accepted:
			[]string{`SDKVersion = "1.47.0"`, "\tSDKVersion = \"1.48.0\""},
			// rejected:
			[]string{`SDKName = "temporal-go"`, `SDKVersion := "1.48.0"`})
	})
}

func TestValidateIncrease(t *testing.T) {
	tests := []struct {
		name    string
		current string
		next    string
		valid   bool
	}{
		{name: "patch", current: "1.47.0", next: "1.47.1", valid: true},
		{name: "minor", current: "1.47.9", next: "1.48.0", valid: true},
		{name: "equal", current: "1.47.0", next: "1.47.0"},
		{name: "skip patch", current: "1.47.0", next: "1.47.2"},
		{name: "skip minor", current: "1.47.0", next: "1.49.0"},
		{name: "minor without patch reset", current: "1.47.9", next: "1.48.1"},
		{name: "major", current: "1.47.9", next: "2.0.0"},
		{name: "lower", current: "1.48.0", next: "1.47.9"},

		// Contrib modules live on 0.x lines and may graduate to a stable release.
		{name: "prerelease patch", current: "0.2.0", next: "0.2.1", valid: true},
		{name: "prerelease minor", current: "0.2.1", next: "0.3.0", valid: true},
		{name: "graduate to stable", current: "0.2.1", next: "1.0.0", valid: true},
		{name: "graduate past stable", current: "0.2.1", next: "2.0.0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, next := mustVersion(t, test.current), mustVersion(t, test.next)
			err := validateVersionIncrease(current, next)
			if test.valid && err != nil {
				t.Fatalf("expected version increase to be valid, got %v", err)
			}
			if !test.valid && err == nil {
				t.Fatalf("expected version increase error, got %v", err)
			}
		})
	}
}

func TestLatestReleasedVersion(t *testing.T) {
	tests := []struct {
		name      string
		tags      []string
		tagPrefix string
		want      string
	}{
		{name: "no tags", tagPrefix: "contrib/envconfig/"},
		{
			name:      "highest wins regardless of order",
			tags:      []string{"contrib/envconfig/v1.0.2", "contrib/envconfig/v0.1.0", "contrib/envconfig/v1.0.10"},
			tagPrefix: "contrib/envconfig/",
			want:      "1.0.10",
		},
		{
			name: "SDK tags have no prefix",
			tags: []string{"v1.47.0", "v1.9.0", "v1.48.0"},
			want: "1.48.0",
		},
		{
			name:      "nested modules and prereleases are not releases of this module",
			tags:      []string{"contrib/aws/s3driver/v0.2.1", "contrib/aws/s3driver/awssdkv2/v0.9.0", "contrib/aws/s3driver/v0.3.0-rc.1"},
			tagPrefix: "contrib/aws/s3driver/",
			want:      "0.2.1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotVersion string
			if got := latestReleasedVersion(test.tags, test.tagPrefix); got != nil {
				gotVersion = got.String()
			}
			if gotVersion != test.want {
				t.Fatalf("unexpected latest version: got %q, want %q", gotVersion, test.want)
			}
		})
	}
}

func TestExtractSDKVersion(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		want    string
		wantErr string
	}{
		{
			name: "sole declaration",
			text: "const (\n\tSDKVersion = \"1.47.0\"\n\tSupportedServerVersions = \">=1.0.0 <2.0.0\"\n)\n",
			want: "1.47.0",
		},
		{
			name:    "no declaration",
			text:    "const SDKName = \"temporal-go\"\n",
			wantErr: "found 0",
		},
		{
			name:    "two declarations",
			text:    "SDKVersion = \"1.47.0\"\nSDKVersion = \"1.48.0\"\n",
			wantErr: "found 2",
		},
		{
			name:    "prerelease declaration",
			text:    "SDKVersion = \"1.48.0-rc.1\"\n",
			wantErr: "invalid version",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := extractSDKVersion(test.text)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != test.want {
				t.Fatalf("unexpected version: got %q, want %q", got, test.want)
			}
		})
	}
}

func TestComputeNewVersionFile(t *testing.T) {
	input := `
const (
	SDKVersion = "1.47.0"
	SupportedServerVersions = ">=1.0.0 <2.0.0"
)
`
	got, err := computeNewVersionFile(input, semver{1, 48, 0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `SDKVersion = "1.48.0"`) {
		t.Fatalf("SDKVersion was not replaced:\n%s", got)
	}
	if !strings.Contains(got, `SupportedServerVersions = ">=1.0.0 <2.0.0"`) {
		t.Fatalf("SupportedServerVersions changed:\n%s", got)
	}
}
