package main

import (
	"strings"
	"testing"
)

func TestContribModuleRegexp(t *testing.T) {
	testRegexp(t, contribModuleRE,
		[]string{"contrib/envconfig", "contrib/opentelemetry-v2", "contrib/aws/s3driver/awssdkv2"},
		[]string{
			"contrib",
			"contrib/",
			"/contrib/envconfig",
			"contrib//envconfig",
			"contrib/../internal",
			"contrib/-envconfig",
			"contrib/Envconfig",
			"internal",
			"",
		})
}

func TestContribTargetRejectsInvalidModules(t *testing.T) {
	for _, dir := range []string{"", "contrib", "internal", "/contrib/envconfig", "../contrib/envconfig", "contrib/../internal", "contrib/envconfig/../tally"} {
		assertInvalidModule(t, dir)
	}
}

func TestContribTargetRejectsBackslashSeparators(t *testing.T) {
	for _, dir := range []string{`contrib\envconfig`, `.\contrib\envconfig`, `contrib\..\internal`, `C:\contrib\envconfig`} {
		assertInvalidModule(t, dir)
	}
}

func assertInvalidModule(t *testing.T, dir string) {
	t.Helper()
	if _, err := contribTarget(dir); err == nil || !strings.Contains(err.Error(), "invalid module") {
		t.Errorf("expected %q to be rejected, got %v", dir, err)
	}
}
