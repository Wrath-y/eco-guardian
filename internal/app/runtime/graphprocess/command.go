// Package graphprocess supervises the local-rag dependency without owning its
// health, Task, Graph, or project facts.
package graphprocess

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/zouyi/eco-guardian/internal/packageinfo"
	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

var ErrBundledCommandInvalid = errors.New("bundled local-rag command is invalid")

type VerifiedAssets interface {
	VerifiedEntrypointPath(packageinfo.ComponentKind) (string, bool)
	VerifiedComponentRoot(packageinfo.ComponentKind) (string, bool)
}

type CommandRequest struct {
	Assets              VerifiedAssets
	ApplicationDataRoot string
	Port                uint16
	HostEnvironment     map[string]string
}

type BundledCommand struct {
	Process  platformprocess.Command
	Endpoint string
}

// BuildBundledCommand produces exactly one owned local-rag process. Python is
// passed as a verified child-runtime asset so any sidecars remain descendants
// of local-rag and are never separately discovered or launched by Eco Guardian.
func BuildBundledCommand(request CommandRequest) (BundledCommand, error) {
	if request.Assets == nil || request.Port == 0 {
		return BundledCommand{}, ErrBundledCommandInvalid
	}
	localRAG, ok := request.Assets.VerifiedEntrypointPath(packageinfo.ComponentLocalRAG)
	if !ok {
		return BundledCommand{}, ErrBundledCommandInvalid
	}
	python, ok := request.Assets.VerifiedEntrypointPath(packageinfo.ComponentPythonRuntime)
	if !ok {
		return BundledCommand{}, ErrBundledCommandInvalid
	}
	embedding, ok := request.Assets.VerifiedComponentRoot(packageinfo.ComponentEmbeddingModel)
	if !ok {
		return BundledCommand{}, ErrBundledCommandInvalid
	}
	rerank, ok := request.Assets.VerifiedComponentRoot(packageinfo.ComponentRerankModel)
	if !ok {
		return BundledCommand{}, ErrBundledCommandInvalid
	}
	applicationRoot, err := validateAbsoluteLocalPath(request.ApplicationDataRoot)
	if err != nil {
		return BundledCommand{}, err
	}
	dataRoot := filepath.Join(applicationRoot, "graph")
	workingDirectory := filepath.Dir(localRAG)
	arguments := []string{
		"serve", "--host", "127.0.0.1", "--port", strconv.Itoa(int(request.Port)),
		"--data-dir", dataRoot,
		"--python-executable", python,
		"--embedding-model", embedding,
		"--rerank-model", rerank,
	}
	environment := safeChildEnvironment(request.HostEnvironment)
	command := platformprocess.Command{Executable: localRAG, Arguments: arguments, Environment: environment, WorkingDirectory: workingDirectory}
	if err = platformprocess.ValidateCommand(command); err != nil {
		return BundledCommand{}, ErrBundledCommandInvalid
	}
	return BundledCommand{Process: command, Endpoint: fmt.Sprintf("http://127.0.0.1:%d", request.Port)}, nil
}

func validateAbsoluteLocalPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", ErrBundledCommandInvalid
	}
	return value, nil
}

func safeChildEnvironment(host map[string]string) []string {
	const safeNames = "SYSTEMROOT,WINDIR,TEMP,TMP"
	allowed := strings.Split(safeNames, ",")
	result := []string{"PYTHONDONTWRITEBYTECODE=1", "PYTHONUTF8=1"}
	for _, name := range allowed {
		if value := host[name]; value != "" && !strings.ContainsAny(value, "\r\n\x00") {
			result = append(result, name+"="+value)
		}
	}
	sort.Slice(result, func(left, right int) bool { return strings.ToUpper(result[left]) < strings.ToUpper(result[right]) })
	return result
}

func (command BundledCommand) SafeArguments() []string {
	return []string{
		"serve", "--host", net.IPv4(127, 0, 0, 1).String(), "--port", "<selected>",
		"--data-dir", "<application-data>", "--python-executable", "<verified-package>",
		"--embedding-model", "<verified-package>", "--rerank-model", "<verified-package>",
	}
}
