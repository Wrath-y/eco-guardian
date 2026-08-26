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
	var request packageinfo.CompleteAssemblyRequest
	var repositoryRoot string
	var contractPinsPath string
	flag.StringVar(&request.OutputRoot, "output", "", "new complete-package output directory")
	flag.StringVar(&request.EcoExecutable, "eco-executable", "", "locally built eco-guardian.exe")
	flag.StringVar(&request.EcoGuardian.Version, "eco-version", "", "Eco Guardian SemVer")
	flag.StringVar(&request.EcoGuardian.Build, "eco-build", "", "Eco Guardian build identity")
	flag.StringVar(&request.EcoGuardian.Commit, "eco-commit", "", "Eco Guardian commit identity")
	flag.StringVar(&request.DefaultsRoot, "defaults", "", "local defaults directory")
	flag.StringVar(&request.LicensesRoot, "licenses", "", "local licenses directory")
	flag.StringVar(&request.NoticesRoot, "notices", "", "local notices directory")
	flag.StringVar(&repositoryRoot, "repository-root", ".", "repository root containing pinned contracts")
	flag.StringVar(&contractPinsPath, "contract-pins", packageinfo.RuntimeContractPinsPath, "repository-relative runtime contract pin set")
	localRAG := componentFlags("local-rag", packageinfo.ComponentLocalRAG, "local-rag", "local-rag.exe")
	python := componentFlags("python", packageinfo.ComponentPythonRuntime, "python-runtime", "python.exe")
	embedding := componentFlags("embedding", packageinfo.ComponentEmbeddingModel, "embedding-model", "")
	rerank := componentFlags("rerank", packageinfo.ComponentRerankModel, "rerank-model", "")
	flag.Parse()

	digests, err := buildinfo.EmbeddedAssetDigests()
	if err != nil {
		exit(err)
	}
	request.EcoGuardian.RuntimeStatusSchemaVersion = buildinfo.RuntimeStatusSchemaVersion
	request.EmbeddedAssetDigests = digests
	request.Components = []packageinfo.ComponentSource{*localRAG, *python, *embedding, *rerank}
	pins, err := packageinfo.LoadAndVerifyRuntimeContractPins(repositoryRoot, contractPinsPath)
	if err != nil {
		exit(err)
	}
	request.ContractPins = &pins
	if _, err = packageinfo.AssembleComplete(context.Background(), request); err != nil {
		exit(err)
	}
}

func componentFlags(prefix string, kind packageinfo.ComponentKind, id, defaultEntrypoint string) *packageinfo.ComponentSource {
	value := &packageinfo.ComponentSource{Kind: kind, ID: id, Entrypoint: defaultEntrypoint}
	flag.StringVar(&value.SourceRoot, prefix+"-root", "", "local pinned "+prefix+" directory")
	flag.StringVar(&value.Version, prefix+"-version", "", "pinned "+prefix+" version")
	if defaultEntrypoint != "" {
		flag.StringVar(&value.Entrypoint, prefix+"-entrypoint", defaultEntrypoint, "entrypoint relative to "+prefix+" root")
	}
	return value
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
