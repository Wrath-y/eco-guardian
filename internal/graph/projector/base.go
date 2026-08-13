package projector

type BaseObservation struct {
	Namespace, Version, ContentHash, Status string
	QueryReady                              bool
	Components                              map[string]string
}

func EligibleBase(namespace, version, localHash string, observation BaseObservation) bool {
	if namespace == "" || version == "" || !validHash(localHash) || observation.Namespace != namespace || observation.Version != version || observation.ContentHash != localHash || observation.Status != "ready" || !observation.QueryReady {
		return false
	}
	return observation.Components["graph"] == "ready" && observation.Components["fts"] == "ready"
}
