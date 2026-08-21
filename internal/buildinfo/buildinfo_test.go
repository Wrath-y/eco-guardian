package buildinfo

import (
	"os"
	"os/exec"
	"regexp"
	"testing"
)

func TestCurrentUsesDeterministicDevelopmentDefaults(t *testing.T) {
	withLinkerValues(t, "0.0.0-dev", "development", "unknown", "development")
	info, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "0.0.0-dev" || info.Build != "development" || info.Commit != "unknown" || info.PackageMode != PackageDevelopment || info.RuntimeStatusSchemaVersion != "1.0" {
		t.Fatalf("development info = %#v", info)
	}
	if len(info.EmbeddedAssetDigests) == 0 {
		t.Fatal("development build must identify embedded assets")
	}
}

func TestLinkerInjectedBuildInfo(t *testing.T) {
	if os.Getenv("ECO_BUILDINFO_LINKER_PROBE") == "1" {
		assertLinkerInjectedBuildInfo(t)
		return
	}
	command := exec.Command("go", "test", "-run", "^TestLinkerInjectedBuildInfo$", "-count=1", "-ldflags", "-X github.com/zouyi/eco-guardian/internal/buildinfo.version=1.2.3 -X github.com/zouyi/eco-guardian/internal/buildinfo.build=ci-20260813.1 -X github.com/zouyi/eco-guardian/internal/buildinfo.commit=abc123def -X github.com/zouyi/eco-guardian/internal/buildinfo.packageMode=complete", ".")
	command.Env = append(os.Environ(), "ECO_BUILDINFO_LINKER_PROBE=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("linker-injected build info test failed: %v\n%s", err, output)
	}
}

func assertLinkerInjectedBuildInfo(t *testing.T) {
	info, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "1.2.3" || info.Build != "ci-20260813.1" || info.Commit != "abc123def" || info.PackageMode != PackageComplete {
		t.Fatalf("injected info = %#v", info)
	}
}

func TestCurrentRejectsInvalidLinkerValues(t *testing.T) {
	withLinkerValues(t, "not-semver", "release", "commit", "remote")
	if _, err := Current(); err == nil {
		t.Fatal("invalid linker values were accepted")
	}
}

func TestEmbeddedAssetDigestsAreStableSHA256(t *testing.T) {
	first, err := EmbeddedAssetDigests()
	if err != nil {
		t.Fatal(err)
	}
	second, err := EmbeddedAssetDigests()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) || first["api/openapi.yaml"] == "" || first["migrations/0001_initial.sql"] == "" {
		t.Fatalf("unexpected embedded digest set: %#v", first)
	}
	sha256 := regexp.MustCompile(`^[a-f0-9]{64}$`)
	for name, digest := range first {
		if digest != second[name] || !sha256.MatchString(digest) {
			t.Fatalf("asset %s digest=%q second=%q", name, digest, second[name])
		}
	}
}

func withLinkerValues(t *testing.T, newVersion, newBuild, newCommit, newPackageMode string) {
	t.Helper()
	oldVersion, oldBuild, oldCommit, oldPackageMode := version, build, commit, packageMode
	version, build, commit, packageMode = newVersion, newBuild, newCommit, newPackageMode
	t.Cleanup(func() { version, build, commit, packageMode = oldVersion, oldBuild, oldCommit, oldPackageMode })
}
