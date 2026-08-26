package graphprocess

import (
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/packageinfo"
)

type fakeAssets struct {
	entrypoints map[packageinfo.ComponentKind]string
	roots       map[packageinfo.ComponentKind]string
}

func (assets fakeAssets) VerifiedEntrypointPath(kind packageinfo.ComponentKind) (string, bool) {
	value, ok := assets.entrypoints[kind]
	return value, ok
}

func (assets fakeAssets) VerifiedComponentRoot(kind packageinfo.ComponentKind) (string, bool) {
	value, ok := assets.roots[kind]
	return value, ok
}

func bundledCommandFixture(t *testing.T) CommandRequest {
	t.Helper()
	root := t.TempDir()
	return CommandRequest{
		Assets: fakeAssets{
			entrypoints: map[packageinfo.ComponentKind]string{
				packageinfo.ComponentLocalRAG:      filepath.Join(root, "package", "local-rag", "local-rag.exe"),
				packageinfo.ComponentPythonRuntime: filepath.Join(root, "package", "python", "python.exe"),
			},
			roots: map[packageinfo.ComponentKind]string{
				packageinfo.ComponentEmbeddingModel: filepath.Join(root, "package", "models", "embedding"),
				packageinfo.ComponentRerankModel:    filepath.Join(root, "package", "models", "rerank"),
			},
		},
		ApplicationDataRoot: filepath.Join(root, "application-data"), Port: 43123,
		HostEnvironment: map[string]string{"SYSTEMROOT": `C:\Windows`, "PATH": "must-not-pass", "API_TOKEN": "fixture-secret", "TEMP": filepath.Join(root, "temp")},
	}
}

func TestBuildBundledCommandUsesOnlyVerifiedAssetsAndLoopback(t *testing.T) {
	request := bundledCommandFixture(t)
	built, err := BuildBundledCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	if built.Endpoint != "http://127.0.0.1:43123" || built.Process.Executable != request.Assets.(fakeAssets).entrypoints[packageinfo.ComponentLocalRAG] {
		t.Fatalf("built=%#v", built)
	}
	joined := strings.Join(append(append([]string{}, built.Process.Arguments...), built.Process.Environment...), " ")
	for _, forbidden := range []string{"fixture-secret", "API_TOKEN", "must-not-pass"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("command contains forbidden value %q: %s", forbidden, joined)
		}
	}
	if strings.Count(joined, "python.exe") != 1 || !strings.Contains(joined, "--python-executable") {
		t.Fatalf("Python must be delegated to local-rag: %s", joined)
	}
	if got := built.SafeArguments(); strings.Contains(strings.Join(got, " "), request.ApplicationDataRoot) || !reflect.DeepEqual(got, built.SafeArguments()) {
		t.Fatalf("unsafe or unstable summary=%v", got)
	}
}

func TestBuildBundledCommandRejectsUnverifiedMissingAndUnsafeInputs(t *testing.T) {
	tests := []func(*CommandRequest){
		func(request *CommandRequest) { request.Port = 0 },
		func(request *CommandRequest) { request.ApplicationDataRoot = "relative" },
		func(request *CommandRequest) {
			delete(request.Assets.(fakeAssets).entrypoints, packageinfo.ComponentLocalRAG)
		},
		func(request *CommandRequest) {
			delete(request.Assets.(fakeAssets).entrypoints, packageinfo.ComponentPythonRuntime)
		},
		func(request *CommandRequest) {
			delete(request.Assets.(fakeAssets).roots, packageinfo.ComponentEmbeddingModel)
		},
		func(request *CommandRequest) {
			delete(request.Assets.(fakeAssets).roots, packageinfo.ComponentRerankModel)
		},
	}
	for index, mutate := range tests {
		request := bundledCommandFixture(t)
		mutate(&request)
		if _, err := BuildBundledCommand(request); !errors.Is(err, ErrBundledCommandInvalid) {
			t.Fatalf("case %d err=%v", index, err)
		}
	}
}

func TestSafeChildEnvironmentIsDeterministicAndSecretFree(t *testing.T) {
	environment := safeChildEnvironment(map[string]string{"TMP": "tmp", "WINDIR": "windows", "AUTHORIZATION": "Bearer secret", "SYSTEMROOT": "root", "TEMP": "temp"})
	want := []string{"PYTHONDONTWRITEBYTECODE=1", "PYTHONUTF8=1", "SYSTEMROOT=root", "TEMP=temp", "TMP=tmp", "WINDIR=windows"}
	if !reflect.DeepEqual(environment, want) {
		t.Fatalf("environment=%v want=%v on %s", environment, want, runtime.GOOS)
	}
}
