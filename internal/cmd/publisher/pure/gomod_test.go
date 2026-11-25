package pure

import (
	"fmt"
	"reflect"
	"slices"
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

func TestNewValidatedGoMod(t *testing.T) {
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
			_, err := NewValidatedGoMod(test.goMod, modulePath)
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

func TestNewValidatedGoModReturnsTemporalRequirements(t *testing.T) {
	goMod := stripIndentation(`
		module go.temporal.io/sdk/contrib/tally

		require (
			go.temporal.io/api v1.63.4
			go.temporal.io/sdk v1.48.0
			example.com/dependency v1.0.0
		)
	`)

	validated, err := NewValidatedGoMod(goMod, "go.temporal.io/sdk/contrib/tally")
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, validated.ModulePath, "go.temporal.io/sdk/contrib/tally")
	var got strings.Builder
	for _, requirement := range validated.Requirements {
		fmt.Fprintf(&got, "%s %s\n", requirement.ModulePath, requirement.Version)
	}
	testEqual(t, got.String(), `
		go.temporal.io/api v1.63.4
		go.temporal.io/sdk v1.48.0
	`)
}

func TestSortGoMods(t *testing.T) {
	input := make(map[string]string)
	input["go.mod"] = stripIndentation(`
		module go.temporal.io/sdk

		require go.temporal.io/api v1.64.3
	`)
	input["contrib/envconfig/go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig

		require (
			go.temporal.io/sdk v1.39.0
			go.temporal.io/api v1.64.3 // indirect
		)
	`)
	input["contrib/aws/secretsmanager/go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/aws/secretsmanager

		require (
			go.temporal.io/sdk v1.39.0
			go.temporal.io/sdk/contrib/envconfig v1.1.0
			go.temporal.io/api v1.64.3 // indirect
		)
	`)
	input["contrib/opentelemetry/go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/opentelemetry

		require (
			go.temporal.io/sdk v1.46.0
			go.temporal.io/api v1.64.3 // indirect
		)
	`)
	input["contrib/opentelemetry/otlpworker/go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/opentelemetry/otlpworker

		require (
			go.temporal.io/sdk v1.46.0
			go.temporal.io/sdk/contrib/opentelemetry v0.8.1
			go.temporal.io/api v1.64.3 // indirect
		)
	`)
	modules := make(map[string]GoMod)
	for file, contents := range input {
		modulePath := SDKModulePath
		if file != "go.mod" {
			modulePath += "/" + strings.TrimSuffix(file, "/go.mod")
		}
		module, err := NewValidatedGoMod(contents, modulePath)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		modules[file] = module
	}
	sdk := modules["go.mod"]
	envconfig := modules["contrib/envconfig/go.mod"]
	secretsmanager := modules["contrib/aws/secretsmanager/go.mod"]
	opentelemetry := modules["contrib/opentelemetry/go.mod"]
	otlpworker := modules["contrib/opentelemetry/otlpworker/go.mod"]
	for _, test := range []struct {
		name    string
		input   []GoMod
		want    string
		wantErr string
	}{
		{name: "nil"},
		{name: "empty", input: []GoMod{}},
		{name: "SDK with external API requirement", input: []GoMod{sdk}, want: "go.temporal.io/sdk"},
		{
			name:  "Secrets Manager dependency chain",
			input: []GoMod{secretsmanager, envconfig, sdk},
			want: `
				go.temporal.io/sdk
				go.temporal.io/sdk/contrib/envconfig
				go.temporal.io/sdk/contrib/aws/secretsmanager
			`,
		},
		{
			name:  "independent contrib modules without SDK in input",
			input: []GoMod{envconfig, opentelemetry},
			want:  "go.temporal.io/sdk/contrib/envconfig go.temporal.io/sdk/contrib/opentelemetry",
		},
		{
			name:  "contrib modules sharing the SDK",
			input: []GoMod{envconfig, opentelemetry, sdk},
			want: `
				go.temporal.io/sdk
				go.temporal.io/sdk/contrib/envconfig go.temporal.io/sdk/contrib/opentelemetry
			`,
		},
		{
			name:  "minimum partitioning across AWS and OpenTelemetry",
			input: []GoMod{secretsmanager, envconfig, sdk, otlpworker, opentelemetry},
			want: `
				go.temporal.io/sdk
				go.temporal.io/sdk/contrib/envconfig go.temporal.io/sdk/contrib/opentelemetry
				go.temporal.io/sdk/contrib/aws/secretsmanager go.temporal.io/sdk/contrib/opentelemetry/otlpworker
			`,
		},
		{
			name:  "Secrets Manager requires a nonadjacent module in the partition",
			input: []GoMod{envconfig, opentelemetry, secretsmanager},
			want: `
				go.temporal.io/sdk/contrib/envconfig go.temporal.io/sdk/contrib/opentelemetry
				go.temporal.io/sdk/contrib/aws/secretsmanager
			`,
		},
		{
			name: "disconnected dependency chains",
			input: []GoMod{
				{ModulePath: "C"},
				{ModulePath: "B", Requirements: []ModuleRequirement{{ModulePath: "C"}}},
				{ModulePath: "A", Requirements: []ModuleRequirement{{ModulePath: "B"}}},
				{ModulePath: "D"},
				{ModulePath: "E", Requirements: []ModuleRequirement{{ModulePath: "D"}}},
			},
			want: `
				C D
				B E
				A
			`,
		},
		{
			name: "deepest dependency is not the last requirement",
			input: []GoMod{
				{ModulePath: "A", Requirements: []ModuleRequirement{{ModulePath: "B"}, {ModulePath: "C"}}},
				{ModulePath: "B", Requirements: []ModuleRequirement{{ModulePath: "D"}}},
				{ModulePath: "C"},
				{ModulePath: "D"},
			},
			want: `
				C D
				B
				A
			`,
		},
		{
			name:    "duplicate module",
			input:   []GoMod{sdk, sdk},
			wantErr: `duplicate module path "go.temporal.io/sdk"`,
		},
		{
			name:    "missing module path",
			input:   []GoMod{{}},
			wantErr: "go.mod at index 0 has no module path",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := slices.Clone(test.input)
			for i := range before {
				before[i].Requirements = slices.Clone(before[i].Requirements)
			}
			got, err := SortGoMods(test.input)
			if !reflect.DeepEqual(test.input, before) {
				t.Fatalf("input changed: got %#v, want %#v", test.input, before)
			}
			if test.wantErr != "" {
				if err == nil {
					t.Fatal("expected an error")
				}
				testEqual(t, err.Error(), test.wantErr)
				if got != nil {
					t.Fatalf("returned a partial ordering on error: %#v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var partitions []string
			for _, partition := range got {
				if len(partition) == 0 {
					t.Fatal("returned an empty partition")
				}
				var paths []string
				for _, module := range partition {
					paths = append(paths, module.ModulePath)
					if i := slices.IndexFunc(test.input, func(input GoMod) bool {
						return input.ModulePath == module.ModulePath
					}); i < 0 || !reflect.DeepEqual(module, test.input[i]) {
						t.Fatalf("module data changed: %#v", module)
					}
				}
				partitions = append(partitions, strings.Join(paths, " "))
			}
			testEqual(t, strings.Join(partitions, "\n"), test.want)
			if len(got) > 0 {
				got[0][0].ModulePath = "changed"
				if !reflect.DeepEqual(test.input, before) {
					t.Fatal("sorted slice shares its backing array with the input")
				}
			}
		})
	}
}
