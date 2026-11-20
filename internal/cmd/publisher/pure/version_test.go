package pure

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
			if got := LatestReleasedVersion(test.tags, test.tagPrefix); got != nil {
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
			got, err := ExtractSDKVersion(test.text)
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

func TestValidateVersionConstant(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected Version
		wantErr  string
	}{
		{
			name:     "matches",
			text:     "SDKVersion = \"1.48.0\"\n",
			expected: Version{1, 48, 0},
		},
		{
			name:     "stale declaration",
			text:     "SDKVersion = \"1.47.0\"\n",
			expected: Version{1, 48, 0},
			wantErr:  "internal/version.go declares version 1.47.0, expected 1.48.0",
		},
		{
			name:     "missing declaration",
			expected: Version{1, 48, 0},
			wantErr:  "expected exactly one SDKVersion declaration in version.go, found 0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateVersionConstant(test.text, SDKModule(), test.expected)
			if test.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != test.wantErr {
				t.Fatalf("got %v, want %q", err, test.wantErr)
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
	got, err := ComputeNewVersionFile(input, Version{1, 48, 0})
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
