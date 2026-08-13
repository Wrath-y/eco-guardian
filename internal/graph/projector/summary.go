package projector

import "fmt"

type Summary struct {
	ProjectID, RevisionID, ConfigHash string
	SchemaVersion                     ProjectionSchemaVersion
	ProjectorVersion                  ProjectorVersion
	ManifestHash                      string
	NodeCount, EdgeCount              int
	CacheIdentity                     string
}

func NewSummary(projectID, revisionID, configHash string, descriptor Descriptor, result Result, cacheIdentity string) (Summary, error) {
	if !descriptor.Valid() || !validHash(configHash) || projectID == "" || revisionID == "" {
		return Summary{}, fmt.Errorf("invalid projection identity")
	}
	_, hash, err := ManifestBytes(result)
	if err != nil {
		return Summary{}, err
	}
	return Summary{ProjectID: projectID, RevisionID: revisionID, ConfigHash: configHash, SchemaVersion: descriptor.SchemaVersion, ProjectorVersion: descriptor.Version, ManifestHash: hash, NodeCount: len(result.Nodes), EdgeCount: len(result.Edges), CacheIdentity: cacheIdentity}, nil
}
func (s Summary) Valid() bool {
	return s.ProjectID != "" && s.RevisionID != "" && validHash(s.ConfigHash) && s.SchemaVersion != "" && s.ProjectorVersion != "" && validHash(s.ManifestHash) && s.NodeCount >= 0 && s.EdgeCount >= 0
}
