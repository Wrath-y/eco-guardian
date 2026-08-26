package planner

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/graph/impact"
)

func TestCanonicalInputGolden(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(current), "..", "..", "..", "..", "tests", "fixtures", "dependency-impact-v1")
	data, err := os.ReadFile(filepath.Join(root, "input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var input impact.Input
	if err = json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	normalized, canonical, hash, err := Normalize(input)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(root, "input.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	if hash != strings.TrimSpace(string(want)) {
		t.Fatalf("hash=%s canonical=%s", hash, canonical)
	}
	if len(normalized.Filters.NodeTypes) != 2 || normalized.Filters.NodeTypes[0] != "attribute" || normalized.Filters.EdgeTypes[0] != "character_has_skill" {
		t.Fatalf("normalized=%#v", normalized)
	}
}

func TestCanonicalInputHashMatchesAcrossProcesses(t *testing.T) {
	if os.Getenv("ECO_IMPACT_HASH_CHILD") == "1" {
		_, current, _, _ := runtime.Caller(0)
		data, err := os.ReadFile(filepath.Join(filepath.Dir(current), "..", "..", "..", "..", "tests", "fixtures", "dependency-impact-v1", "input.json"))
		if err != nil {
			t.Fatal(err)
		}
		var input impact.Input
		if err = json.Unmarshal(data, &input); err != nil {
			t.Fatal(err)
		}
		_, _, hash, err := Normalize(input)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("IMPACT_HASH=" + hash)
		return
	}
	run := func() string {
		command := exec.Command(os.Args[0], "-test.run=^TestCanonicalInputHashMatchesAcrossProcesses$")
		command.Env = append(os.Environ(), "ECO_IMPACT_HASH_CHILD=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("child: %v\n%s", err, output)
		}
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "IMPACT_HASH=") {
				return strings.TrimPrefix(line, "IMPACT_HASH=")
			}
		}
		t.Fatalf("missing child hash: %s", output)
		return ""
	}
	first, second := run(), run()
	if first != second || !impact.ValidHash(first) {
		t.Fatalf("process hashes=%q/%q", first, second)
	}
}
