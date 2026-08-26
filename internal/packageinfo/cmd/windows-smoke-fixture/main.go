// Command windows-smoke-fixture prepares a project outside staged artifacts
// for clean-VM acceptance. It is a test harness binary, never package content.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
)

type fixedSelector string

func (selector fixedSelector) SelectDirectory(context.Context) (string, error) {
	return string(selector), nil
}

func main() {
	var localAppData, projectDirectory string
	var reopenOnly bool
	flag.StringVar(&localAppData, "local-app-data", "", "isolated LOCALAPPDATA used by the smoke test")
	flag.StringVar(&projectDirectory, "project", "", "new smoke project directory")
	flag.BoolVar(&reopenOnly, "reopen-only", false, "open the existing recent project without creating it")
	flag.Parse()
	if localAppData == "" || projectDirectory == "" {
		exit(fmt.Errorf("local-app-data and project are required"))
	}
	localAppData, projectDirectory = filepath.Clean(localAppData), filepath.Clean(projectDirectory)
	if !filepath.IsAbs(localAppData) || !filepath.IsAbs(projectDirectory) {
		exit(fmt.Errorf("smoke paths must be absolute"))
	}
	if err := os.MkdirAll(projectDirectory, 0o700); err != nil {
		exit(err)
	}
	registry, err := domain.NewRegistry()
	if err != nil {
		exit(err)
	}
	manager := project.NewManager(
		project.NewTokenStore(time.Minute, nil), project.FileLocker{}, project.SQLiteFactory{Registry: registry}, project.NoJobs{}, project.NewFileRecentProjects(localAppData),
	)
	if reopenOnly {
		recent, recentErr := manager.Recent()
		if recentErr != nil {
			exit(fmt.Errorf("recent project is unavailable: %w", recentErr))
		}
		if len(recent) == 0 {
			exit(fmt.Errorf("recent project is unavailable"))
		}
		reopened, reopenErr := manager.OpenRecent(context.Background(), recent[0].ID)
		if reopenErr != nil {
			exit(fmt.Errorf("reopen existing project: %w", reopenErr))
		}
		if err = manager.Close(context.Background()); err != nil {
			exit(err)
		}
		info, statErr := os.Stat(filepath.Join(projectDirectory, "project.db"))
		if statErr != nil || info.Size() == 0 {
			exit(fmt.Errorf("existing project database is unavailable: %w", statErr))
		}
		if err = json.NewEncoder(os.Stdout).Encode(map[string]any{"project_id": reopened.ID, "database_size": info.Size(), "reopened": true}); err != nil {
			exit(err)
		}
		return
	}
	token, _, err := manager.IssueSelection(context.Background(), fixedSelector(projectDirectory))
	if err != nil {
		exit(err)
	}
	created, err := manager.Create(context.Background(), token)
	if err != nil {
		exit(err)
	}
	if err = manager.Close(context.Background()); err != nil {
		exit(err)
	}
	reopened, err := manager.OpenRecent(context.Background(), created.ID)
	if err != nil {
		exit(fmt.Errorf("reopen prepared project: %w", err))
	}
	if reopened.ID != created.ID {
		exit(fmt.Errorf("reopened project identity does not match"))
	}
	if err = manager.Close(context.Background()); err != nil {
		exit(err)
	}
	info, err := os.Stat(filepath.Join(projectDirectory, "project.db"))
	if err != nil || info.Size() == 0 {
		exit(fmt.Errorf("prepared project database is unavailable: %w", err))
	}
	if err = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"project_id": created.ID, "project_name": created.Name, "database_size": info.Size(),
	}); err != nil {
		exit(err)
	}
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
