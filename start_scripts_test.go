//go:build !windows

package ecoguardian

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartScriptReusesHealthyLocalRAGAndForwardsArguments(t *testing.T) {
	testStartScript(t, true)
}

func TestStartScriptStartsUnhealthyLocalRAGBeforeEcoGuardian(t *testing.T) {
	testStartScript(t, false)
}

func testStartScript(t *testing.T, initiallyHealthy bool) {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	bin := filepath.Join(temporary, "bin")
	localRAG := filepath.Join(temporary, "local-rag")
	if err = os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(localRAG, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(temporary, "local-rag.healthy")
	stopped := filepath.Join(temporary, "local-rag.stopped")
	arguments := filepath.Join(temporary, "go.arguments")
	buildArguments := filepath.Join(temporary, "go-build.arguments")
	webBuildArguments := filepath.Join(temporary, "npm-build.arguments")
	runtimeDir := filepath.Join(temporary, "runtime")
	if err = os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if initiallyHealthy {
		if err = os.WriteFile(marker, []byte("healthy"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable(t, filepath.Join(bin, "curl"), "#!/bin/sh\n[ -f \"$ECO_TEST_HEALTH_MARKER\" ]\n")
	writeExecutable(t, filepath.Join(bin, "go"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ECO_TEST_BUILD_ARGUMENTS\"\n")
	writeExecutable(t, filepath.Join(bin, "npm"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ECO_TEST_WEB_BUILD_ARGUMENTS\"\n")
	writeExecutable(t, filepath.Join(runtimeDir, "eco-guardian"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ECO_TEST_ARGUMENTS\"\n")
	writeExecutable(t, filepath.Join(localRAG, "start.sh"), "#!/bin/sh\n: > \"$ECO_TEST_HEALTH_MARKER\"\n")
	writeExecutable(t, filepath.Join(localRAG, "stop.sh"), "#!/bin/sh\n: > \"$ECO_TEST_STOPPED_MARKER\"\n")

	command := exec.Command("bash", filepath.Join(root, "start.sh"), "--browser-auto-open", "false")
	command.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"LOCAL_RAG_DIR="+localRAG,
		"ECO_GUARDIAN_GRAPH_ENDPOINT=http://127.0.0.1:9876/",
		"ECO_GUARDIAN_RUNTIME_DIR="+runtimeDir,
		"ECO_TEST_HEALTH_MARKER="+marker,
		"ECO_TEST_STOPPED_MARKER="+stopped,
		"ECO_TEST_ARGUMENTS="+arguments,
		"ECO_TEST_BUILD_ARGUMENTS="+buildArguments,
		"ECO_TEST_WEB_BUILD_ARGUMENTS="+webBuildArguments,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("start.sh failed: %v\n%s", err, output)
	}
	if !initiallyHealthy {
		if _, err = os.Stat(marker); err != nil {
			t.Fatalf("local-rag launcher was not called: %v", err)
		}
		if _, err = os.Stat(stopped); err != nil {
			t.Fatalf("owned local-rag process was not cleaned up: %v", err)
		}
	} else if _, err = os.Stat(stopped); !os.IsNotExist(err) {
		t.Fatalf("pre-existing local-rag process should not be stopped: %v", err)
	}
	got, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	want := "--graph-endpoint\nhttp://127.0.0.1:9876\n--browser-auto-open\nfalse\n"
	if string(got) != want {
		t.Fatalf("forwarded arguments:\n%s\nwant:\n%s", got, want)
	}
	gotBuild, err := os.ReadFile(buildArguments)
	if err != nil {
		t.Fatal(err)
	}
	wantBuild := strings.Join([]string{"build", "-o", filepath.Join(runtimeDir, "eco-guardian"), "./cmd/eco-guardian", ""}, "\n")
	if string(gotBuild) != wantBuild {
		t.Fatalf("build arguments:\n%s\nwant:\n%s", gotBuild, wantBuild)
	}
	gotWebBuild, err := os.ReadFile(webBuildArguments)
	if err != nil {
		t.Fatal(err)
	}
	wantWebBuild := strings.Join([]string{"--prefix", filepath.Join(root, "web"), "run", "build", ""}, "\n")
	if string(gotWebBuild) != wantWebBuild {
		t.Fatalf("web build arguments:\n%s\nwant:\n%s", gotWebBuild, wantWebBuild)
	}
}

func TestWindowsStartScriptPreservesCombinedLauncherContract(t *testing.T) {
	data, err := os.ReadFile("start.bat")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"set \"LOCAL_RAG_ROOT=%SCRIPT_DIR%..\\local-rag\"",
		"call \"!LOCAL_RAG_ROOT!\\start.bat\"",
		"call \"!LOCAL_RAG_ROOT!\\stop.bat\"",
		"curl.exe --fail --silent --show-error --max-time 3 \"!GRAPH_ENDPOINT!/health\"",
		"call npm.cmd --prefix \"%SCRIPT_DIR%web\" run build",
		"go build -o \"!ECO_GUARDIAN_BINARY!\" .\\cmd\\eco-guardian",
		"\"!ECO_GUARDIAN_BINARY!\" --graph-endpoint \"!GRAPH_ENDPOINT!\" %*",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("start.bat is missing %q", required)
		}
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}
