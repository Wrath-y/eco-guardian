package graphprocess

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

const processSummarySchemaVersion = 1

type SummaryStore interface {
	SaveProcessSummary(ProcessObservation) error
}

type FileSummaryStore struct {
	mu       sync.Mutex
	filename string
}

type persistedSummary struct {
	SchemaVersion int                `json:"schema_version"`
	Observation   ProcessObservation `json:"observation"`
}

func NewFileSummaryStore(filename string) *FileSummaryStore {
	return &FileSummaryStore{filename: filename}
}

func (store *FileSummaryStore) SaveProcessSummary(observation ProcessObservation) error {
	if store == nil || strings.TrimSpace(store.filename) == "" || !validSummary(observation) {
		return ErrSupervisorInvalid
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	body, err := json.Marshal(persistedSummary{SchemaVersion: processSummarySchemaVersion, Observation: observation})
	if err != nil {
		return err
	}
	directory := filepath.Dir(store.filename)
	if err = os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".process-summary-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(append(body, '\n'))
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	backup := temporaryName + ".previous"
	if _, statErr := os.Stat(store.filename); statErr == nil {
		if err = os.Rename(store.filename, backup); err != nil {
			return err
		}
	}
	if err = os.Rename(temporaryName, store.filename); err != nil {
		_ = os.Rename(backup, store.filename)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

func (store *FileSummaryStore) LoadProcessSummary() (ProcessObservation, error) {
	if store == nil {
		return ProcessObservation{}, ErrSupervisorInvalid
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	body, err := os.ReadFile(store.filename)
	if err != nil {
		return ProcessObservation{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var value persistedSummary
	if err = decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF || value.SchemaVersion != processSummarySchemaVersion || !validSummary(value.Observation) {
		return ProcessObservation{}, errors.New("invalid process summary")
	}
	return value.Observation, nil
}

func validSummary(value ProcessObservation) bool {
	if !validSupervisorState(value.State) || value.Sequence == 0 || value.ObservedAt.IsZero() || strings.ContainsAny(value.Reason, "\r\n\x00") || len(value.Reason) > 128 {
		return false
	}
	if value.Ownership != platformprocess.OwnershipNone && value.Ownership != platformprocess.OwnershipExternal && value.Ownership != platformprocess.OwnershipBundled {
		return false
	}
	if value.Endpoint == "" {
		return true
	}
	endpoint, err := url.Parse(value.Endpoint)
	if err != nil || endpoint.Scheme != "http" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(endpoint.Hostname()), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validSupervisorState(value SupervisorState) bool {
	switch value {
	case StateNotSelected, StateExternal, StateStarting, StateReady, StateBackoff, StateExited, StateRestartExhausted, StateStopping:
		return true
	default:
		return false
	}
}
