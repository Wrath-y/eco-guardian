package backupdomain

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestCanonicalBackupFixturesAreByteStable(t *testing.T) {
	root := filepath.Join("..", "..", "..", "api", "fixtures", "backup-v1")
	files := []string{"manifest.json", "backup-result.json", "daily-admission-waiver.json", "inventory-record.json", "restore-preflight.json", "restore-journal.json", "idempotent-commands.json", "problems.json", "job-events.json"}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			encoded, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err = json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			canonical, err := domain.CanonicalJSON(value)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bytes.TrimSpace(encoded), canonical) {
				t.Fatalf("fixture is not canonical\nfile: %s\nwant: %s", encoded, canonical)
			}
		})
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := DecodeManifest(bytes.TrimSpace(manifestBytes))
	if err != nil || manifest.ManifestHash != "36d836a56293d29e2e4d76276fa69f5628be5d58707530089f6ebeb39267f5ab" {
		t.Fatalf("golden manifest rejected: %#v %v", manifest, err)
	}
}
