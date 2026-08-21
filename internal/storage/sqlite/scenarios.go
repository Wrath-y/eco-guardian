package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

// ScenarioDefinition is the persisted immutable record. The typed Template is
// reconstructed from its canonical body on every read.
type ScenarioDefinition struct {
	ID                 domain.ID
	ProjectID          domain.ID
	SourceDefinitionID domain.ID
	Template           scenario.Template
	CreatedAt          time.Time
}

func seedBuiltinScenarioDefinitions(ctx context.Context, tx *sql.Tx) error {
	var projectID domain.ID
	if err := tx.QueryRowContext(ctx, `SELECT id FROM project_meta`).Scan(&projectID); err != nil {
		return err
	}
	for _, template := range scenario.BuiltinTemplates() {
		id, err := domain.NewID()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scenario_definitions(id,project_uuid,scene_id,scene_version,origin,source_definition_id,canonical_body,canonical_hash,created_at)
			VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(project_uuid,scene_id,scene_version) DO NOTHING`, id, projectID, template.Definition.ID, template.Definition.Version, template.Origin, nil, string(template.Body), template.BodyHash, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetScenarioDefinition(ctx context.Context, sceneID, version string) (ScenarioDefinition, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,project_uuid,source_definition_id,origin,canonical_body,canonical_hash,created_at FROM scenario_definitions WHERE project_uuid=? AND scene_id=? AND scene_version=?`, s.projectID, sceneID, version)
	return scanScenarioDefinition(row)
}

func (s *Store) ListScenarioDefinitions(ctx context.Context) ([]ScenarioDefinition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,project_uuid,source_definition_id,origin,canonical_body,canonical_hash,created_at FROM scenario_definitions WHERE project_uuid=? ORDER BY scene_id,scene_version,id`, s.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	definitions := make([]ScenarioDefinition, 0)
	for rows.Next() {
		definition, err := scanScenarioDefinition(rows)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, rows.Err()
}

// CloneScenarioDefinition persists a new immutable scene/version. The caller
// supplies the revision-bound participant resolver, keeping this SQLite
// adapter independent from mutable configuration state.
func (s *Store) CloneScenarioDefinition(ctx context.Context, sourceID, sourceVersion, sceneID, version string, overlays []scenario.ParameterOverlay, participantExists func(string) bool) (ScenarioDefinition, error) {
	source, err := s.GetScenarioDefinition(ctx, sourceID, sourceVersion)
	if err != nil {
		return ScenarioDefinition{}, err
	}
	template, err := scenario.Clone(source.Template, sceneID, version, overlays, participantExists)
	if err != nil {
		return ScenarioDefinition{}, err
	}
	id, err := domain.NewID()
	if err != nil {
		return ScenarioDefinition{}, err
	}
	createdAt := s.now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO scenario_definitions(id,project_uuid,scene_id,scene_version,origin,source_definition_id,canonical_body,canonical_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, s.projectID, sceneID, version, template.Origin, source.ID, string(template.Body), template.BodyHash, createdAt.Format(time.RFC3339Nano))
	if err != nil {
		return ScenarioDefinition{}, err
	}
	return ScenarioDefinition{ID: id, ProjectID: s.projectID, SourceDefinitionID: source.ID, Template: template, CreatedAt: createdAt}, nil
}

type scenarioScanner interface{ Scan(...any) error }

func scanScenarioDefinition(scanner scenarioScanner) (ScenarioDefinition, error) {
	var definition ScenarioDefinition
	var origin, body, bodyHash, createdAt string
	var sourceID sql.NullString
	if err := scanner.Scan(&definition.ID, &definition.ProjectID, &sourceID, &origin, &body, &bodyHash, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return ScenarioDefinition{}, ErrNotFound
		}
		return ScenarioDefinition{}, err
	}
	template, err := scenarioTemplate(body, origin, bodyHash)
	if err != nil {
		return ScenarioDefinition{}, err
	}
	definition.Template = template
	if sourceID.Valid {
		definition.SourceDefinitionID = domain.ID(sourceID.String)
	}
	definition.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil || !definition.ID.Valid() || !definition.ProjectID.Valid() || (definition.Template.Origin == "builtin" && definition.SourceDefinitionID != "") || (definition.Template.Origin == "clone" && !definition.SourceDefinitionID.Valid()) {
		return ScenarioDefinition{}, fmt.Errorf("invalid stored scenario definition")
	}
	return definition, nil
}

func scenarioTemplate(body, origin, bodyHash string) (scenario.Template, error) {
	definition, err := scenario.ParseDefinition([]byte(body))
	if err != nil {
		return scenario.Template{}, err
	}
	if origin != "builtin" && origin != "clone" {
		return scenario.Template{}, fmt.Errorf("invalid stored scenario origin")
	}
	digest := sha256.Sum256([]byte(body))
	if bodyHash != hex.EncodeToString(digest[:]) {
		return scenario.Template{}, fmt.Errorf("invalid stored scenario hash")
	}
	return scenario.Template{Definition: definition, Origin: origin, Body: []byte(body), BodyHash: bodyHash}, nil
}
