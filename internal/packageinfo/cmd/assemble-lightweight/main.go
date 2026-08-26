package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/zouyi/eco-guardian/internal/buildinfo"
	"github.com/zouyi/eco-guardian/internal/packageinfo"
)

func main() {
	var request packageinfo.LightweightAssemblyRequest
	flag.StringVar(&request.OutputRoot, "output", "", "new lightweight-package output directory")
	flag.StringVar(&request.EcoExecutable, "eco-executable", "", "locally built eco-guardian.exe")
	flag.StringVar(&request.EcoGuardian.Version, "eco-version", "", "Eco Guardian SemVer")
	flag.StringVar(&request.EcoGuardian.Build, "eco-build", "", "Eco Guardian build identity")
	flag.StringVar(&request.EcoGuardian.Commit, "eco-commit", "", "Eco Guardian commit identity")
	flag.StringVar(&request.ConfigurationTemplate, "config-template", "", "local settings template")
	flag.StringVar(&request.LicensesRoot, "licenses", "", "local licenses directory")
	flag.StringVar(&request.NoticesRoot, "notices", "", "local notices directory")
	flag.Parse()
	digests, err := buildinfo.EmbeddedAssetDigests()
	if err != nil {
		exit(err)
	}
	request.EcoGuardian.RuntimeStatusSchemaVersion = buildinfo.RuntimeStatusSchemaVersion
	request.EmbeddedAssetDigests = digests
	if _, err = packageinfo.AssembleLightweight(context.Background(), request); err != nil {
		exit(err)
	}
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
