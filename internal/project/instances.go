package project

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	instanceOwnerVersion  = 1
	instanceOwnerMaxBytes = 4096
	InstanceTokenHeader   = "X-Eco-Instance-Token"
	instanceClosePath     = "/api/v1/internal/instances/close"
)

// InstanceOwner is stored in the exclusive project lock file. The secret is
// consumed only by server-to-server loopback requests and is never projected
// to the browser.
type InstanceOwner struct {
	Version  int    `json:"version"`
	PID      int    `json:"pid"`
	URL      string `json:"url"`
	Secret   string `json:"secret"`
	Acquired string `json:"acquired_at"`
}

func NewInstanceSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (owner InstanceOwner) valid() bool {
	if owner.Version != instanceOwnerVersion || owner.PID < 1 || owner.Acquired == "" {
		return false
	}
	secret, err := base64.RawURLEncoding.DecodeString(owner.Secret)
	if err != nil || len(secret) != 32 {
		return false
	}
	endpoint, err := url.Parse(owner.URL)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(endpoint.Port())
	return err == nil && port > 0 && port <= 65535
}

func ReadInstanceOwner(directory string) (InstanceOwner, error) {
	file, err := os.Open(filepath.Join(directory, ".eco-guardian.lock"))
	if err != nil {
		return InstanceOwner{}, fmt.Errorf("%w: %v", ErrLockOwnerUnknown, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, instanceOwnerMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > instanceOwnerMaxBytes {
		return InstanceOwner{}, ErrLockOwnerUnknown
	}
	var owner InstanceOwner
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&owner); err != nil || !owner.valid() {
		return InstanceOwner{}, ErrLockOwnerUnknown
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		return InstanceOwner{}, ErrLockOwnerUnknown
	}
	return owner, nil
}

type PeerInstanceClient struct {
	Client         *http.Client
	IsProcessAlive func(int) (bool, error)
}

func (client PeerInstanceClient) Close(ctx context.Context, info ProjectInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !info.ID.Valid() || info.Path == "" {
		return ErrInvalidSelection
	}
	owner, err := ReadInstanceOwner(info.Path)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"project_id": string(info.ID)})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, owner.URL+instanceClosePath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOtherUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(InstanceTokenHeader, owner.Secret)
	httpClient := client.Client
	if httpClient == nil {
		httpClient = &http.Client{Transport: &http.Transport{Proxy: nil}}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		reclaimed, reclaimErr := client.reclaimStaleLock(info.Path, owner)
		if reclaimErr != nil {
			return fmt.Errorf("%w: %v", ErrOtherUnavailable, reclaimErr)
		}
		if reclaimed {
			return nil
		}
		return fmt.Errorf("%w: %v", ErrOtherUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&problem)
		if problem.Code == "CLOSE_BLOCKED" {
			return ErrCloseBlocked
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return ErrOtherRejected
		}
		return ErrOtherUnavailable
	}
	lockPath := filepath.Join(info.Path, ".eco-guardian.lock")
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, statErr := os.Stat(lockPath); os.IsNotExist(statErr) {
			return nil
		} else if statErr != nil {
			return fmt.Errorf("%w: %v", ErrOtherUnavailable, statErr)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %v", ErrOtherUnavailable, ctx.Err())
		case <-ticker.C:
		}
	}
}

// reclaimStaleLock archives a verified owner record only after the recorded
// process is known to have exited. A live or uninspectable PID keeps the lock,
// so a temporarily unreachable instance is never forcefully displaced.
func (client PeerInstanceClient) reclaimStaleLock(directory string, expected InstanceOwner) (bool, error) {
	probe := client.IsProcessAlive
	if probe == nil {
		probe = processAlive
	}
	alive, err := probe(expected.PID)
	if err != nil || alive {
		return false, err
	}
	current, err := ReadInstanceOwner(directory)
	if err != nil {
		if errors.Is(err, ErrLockOwnerUnknown) {
			if _, statErr := os.Stat(filepath.Join(directory, ".eco-guardian.lock")); errors.Is(statErr, os.ErrNotExist) {
				return true, nil
			}
		}
		return false, err
	}
	if current != expected {
		return false, errors.New("project lock owner changed while closing the other instance")
	}
	lockPath := filepath.Join(directory, ".eco-guardian.lock")
	archivePath := fmt.Sprintf("%s.stale-pid-%d-%s", lockPath, expected.PID, time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err = os.Rename(lockPath, archivePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	return true, nil
}
