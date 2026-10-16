package main

import "testing"

func TestEscapeModulePath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"go.temporal.io/sdk", "go.temporal.io/sdk"},
		{"go.temporal.io/sdk/contrib/aws/s3driver", "go.temporal.io/sdk/contrib/aws/s3driver"},
		{"github.com/BurntSushi/toml", "github.com/!burnt!sushi/toml"},
	}
	for _, test := range tests {
		if got := escapeModulePath(test.in); got != test.want {
			t.Errorf("escapeModulePath(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}
