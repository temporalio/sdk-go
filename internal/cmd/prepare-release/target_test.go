package main

import (
	"strings"
	"testing"
)

func TestSDKTarget(t *testing.T) {
	target := sdkTarget()

	if got := target.tag("1.48.0"); got != "v1.48.0" {
		t.Errorf("unexpected tag: %q", got)
	}
	if got := target.tagPattern(); got != "v*" {
		t.Errorf("unexpected tag pattern: %q", got)
	}
	if got := target.changelogFile(); got != "CHANGELOG.md" {
		t.Errorf("unexpected changelog: %q", got)
	}
	if got := target.goModFile(); got != "go.mod" {
		t.Errorf("unexpected go.mod: %q", got)
	}
	if got := strings.Join(target.releaseFiles(), " "); got != "CHANGELOG.md internal/version.go" {
		t.Errorf("unexpected release files: %q", got)
	}
	args := commandArgs{target: target, version: "1.48.0"}
	if got := releaseBranch(args); got != "chore/release-1.48.0" {
		t.Errorf("unexpected branch: %q", got)
	}
	if got := releaseSubject(args); got != "release 1.48.0" {
		t.Errorf("unexpected subject: %q", got)
	}
	if !target.markLatest {
		t.Error("the main SDK release should be GitHub's latest release")
	}
}

func TestContribTarget(t *testing.T) {
	target, err := contribTarget("contrib/aws/s3driver")
	if err != nil {
		t.Fatal(err)
	}

	if target.modulePath != "go.temporal.io/sdk/contrib/aws/s3driver" {
		t.Errorf("unexpected module path: %q", target.modulePath)
	}
	// Sub-modules are versioned by module-prefixed tags, per https://go.dev/ref/mod#vcs-version.
	if got := target.tag("0.2.2"); got != "contrib/aws/s3driver/v0.2.2" {
		t.Errorf("unexpected tag: %q", got)
	}
	if got := target.tagPattern(); got != "contrib/aws/s3driver/v*" {
		t.Errorf("unexpected tag pattern: %q", got)
	}
	if got := target.changelogFile(); got != "contrib/aws/s3driver/CHANGELOG.md" {
		t.Errorf("unexpected changelog: %q", got)
	}
	if got := target.goModFile(); got != "contrib/aws/s3driver/go.mod" {
		t.Errorf("unexpected go.mod: %q", got)
	}
	// A contrib release never touches the SDK's version constant.
	if got := strings.Join(target.releaseFiles(), " "); got != "contrib/aws/s3driver/CHANGELOG.md" {
		t.Errorf("unexpected release files: %q", got)
	}
	args := commandArgs{target: target, version: "0.2.2"}
	if got := releaseBranch(args); got != "chore/release-contrib-aws-s3driver-0.2.2" {
		t.Errorf("unexpected branch: %q", got)
	}
	if got := releaseSubject(args); got != "contrib/aws/s3driver release 0.2.2" {
		t.Errorf("unexpected subject: %q", got)
	}
	if target.markLatest {
		t.Error("a contrib release must never be GitHub's latest release")
	}
	if target.versionFile != "" {
		t.Errorf("contrib modules do not embed a version: %q", target.versionFile)
	}
}

func TestContribTargetRejectsInvalidModules(t *testing.T) {
	for _, dir := range []string{"", "contrib", "internal", "../contrib/envconfig", "contrib/../internal", "contrib/envconfig/../tally"} {
		_, err := contribTarget(dir)
		if err == nil || !strings.Contains(err.Error(), "invalid module") {
			t.Errorf("expected %q to be rejected, got %v", dir, err)
		}
	}
}

// The tag prefix belongs to exactly one module even when modules nest.
func TestContribTargetTagPatternsDoNotOverlap(t *testing.T) {
	parent, err := contribTarget("contrib/aws/s3driver")
	if err != nil {
		t.Fatal(err)
	}
	nested, err := contribTarget("contrib/aws/s3driver/awssdkv2")
	if err != nil {
		t.Fatal(err)
	}

	if strings.HasPrefix(nested.tag("0.1.0"), parent.tagPrefix+"v") {
		t.Errorf("nested tag %q would be read as a release of %s", nested.tag("0.1.0"), parent.modulePath)
	}
	if latestReleasedVersion([]string{nested.tag("9.9.9")}, parent.tagPrefix) != "" {
		t.Error("nested module tags must not count as releases of the parent module")
	}
}
