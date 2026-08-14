package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zouyi/eco-guardian/internal/app"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	"github.com/zouyi/eco-guardian/internal/httpapi"
	"github.com/zouyi/eco-guardian/internal/project"
)

func main() {
	registry, err := domain.NewRegistry()
	if err != nil {
		log.Fatal(err)
	}
	appData, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	graphDescriptor := projector.Descriptor{SchemaVersion: projector.ProjectionSchemaV1, Version: projector.ProjectorV1, Relations: projector.V1Relations(), Formatter: projector.V1Formatter{}}
	manager := project.NewManager(project.NewTokenStore(5*time.Minute, nil), project.FileLocker{}, project.SQLiteFactory{Registry: registry, GraphVersionContributor: projector.VersionContributor{Descriptor: graphDescriptor}}, project.NoJobs{}, project.NewFileRecentProjects(appData))
	runtime, err := httpapi.NewRuntime("127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	httpapi.NewProjectHandler(manager, project.NativeDirectorySelector{}).Register(runtime.Engine())
	httpapi.NewSchemaHandler(registry).Register(runtime.Engine())
	httpapi.NewEntityHandler(httpapi.StoreFromProjectManager(manager)).Register(runtime.Engine())
	httpapi.NewValidationHandler(httpapi.ValidationStoreFromProjectManager(manager)).Register(runtime.Engine())
	application, err := app.New(app.Config{Runtime: runtime, Projects: manager})
	if err != nil {
		log.Fatal(err)
	}
	if err := application.Start(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Eco Guardian is running at http://%s\n", runtime.Address())
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	if err := application.Close(context.Background()); err != nil {
		log.Print(err)
	}
}
