// Package nvketooling delegates local operator work to the pinned native runner.
package nvketooling

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
)

//go:embed tooling.py
var runner []byte

//go:embed provenance.json
var provenance []byte

type Manifest struct {
	Format           int      `json:"format"`
	SourceRevision   string   `json:"sourceRevision"`
	UpstreamRevision string   `json:"upstreamRevision"`
	RunnerSHA256     string   `json:"runnerSha256"`
	Commands         []string `json:"commands"`
}

type Bundle struct {
	Format              int    `json:"format"`
	Revision            string `json:"revision"`
	SourceArchiveSHA256 string `json:"sourceArchiveSha256"`
	SourceTreeSHA256    string `json:"sourceTreeSha256"`
	BinarySHA256        string `json:"binarySha256"`
	CAPIVersion         string `json:"capiVersion"`
}

type Result struct {
	Format          int    `json:"format"`
	Revision        string `json:"revision"`
	RunID           string `json:"runId"`
	StartedAt       string `json:"startedAt"`
	Phase           string `json:"phase"`
	Kind            string `json:"kind"`
	ArgumentsDigest string `json:"argumentsDigest"`
	ExitCode        int    `json:"exitCode"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
}

func Info() (Manifest, error) {
	var result Manifest
	digest := sha256.Sum256(runner)
	if json.Unmarshal(provenance, &result) != nil || result.Format != 1 ||
		result.UpstreamRevision != nvkecatalog.Pinned().Revision ||
		result.RunnerSHA256 != hex.EncodeToString(digest[:]) || len(result.SourceRevision) != 40 {
		return result, errors.New("embedded NVKE tooling provenance is invalid")
	}
	return result, nil
}

func Prepare(ctx context.Context, python, checkout, output string) (*Bundle, error) {
	data, status, err := invoke(ctx, python, []string{"prepare", "--checkout", checkout, "--output", output}, nil)
	if err != nil {
		return nil, err
	}
	var result Bundle
	if status != 0 || decode(data, &result) != nil || result.Format != 1 || result.Revision != nvkecatalog.Pinned().Revision ||
		len(result.BinarySHA256) != 64 || len(result.SourceTreeSHA256) != 64 || len(result.SourceArchiveSHA256) != 64 || result.CAPIVersion == "" {
		return nil, errors.New("native preparation failed; inspect the private output directory and build log")
	}
	return &result, nil
}

func Run(ctx context.Context, python, bundle, workspace string, arguments []string, input io.Reader) (*Result, error) {
	args := []string{"run", "--bundle", bundle, "--workspace", workspace, "--"}
	data, status, err := invoke(ctx, python, append(args, arguments...), input)
	if err != nil {
		return nil, err
	}
	var result Result
	if decode(data, &result) != nil || result.Format != 1 || result.Revision != nvkecatalog.Pinned().Revision ||
		result.RunID == "" || result.ArgumentsDigest == "" || result.Stdout == "" || result.Stderr == "" ||
		(result.ExitCode&255) != status || (result.Phase != "completed" && result.Phase != "failed" && result.Phase != "uncertain") {
		return nil, errors.New("native run was not confirmed; preserve the workspace and inspect its private artifacts before recovery")
	}
	return &result, nil
}

func decode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return errors.New("unexpected native result data")
	}
	return nil
}

func nativeEnvironment() []string {
	var result []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "ASTROLIFT_") {
			result = append(result, entry)
		}
	}
	return result
}

func pythonRuntime(ctx context.Context, requested string) (string, error) {
	candidates := []string{"python3", "python3.12", "python3.13", "python3.14"}
	if requested != "" {
		candidates = []string{requested}
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		command := exec.CommandContext(ctx, path, "-I", "-c", "import sys; sys.exit(0 if sys.version_info >= (3,12) else 1)")
		command.Env = nativeEnvironment()
		if command.Run() == nil {
			return path, nil
		}
	}
	return "", errors.New("native NVKE operator tooling requires Python 3.12+; install it or select --python")
}

type metadataBuffer struct{ data bytes.Buffer }

func (buffer *metadataBuffer) Write(data []byte) (int, error) {
	if buffer.data.Len()+len(data) > 64*1024 {
		return 0, errors.New("native metadata exceeds its size limit")
	}
	return buffer.data.Write(data)
}

func invoke(ctx context.Context, requestedPython string, args []string, input io.Reader) ([]byte, int, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, 0, errors.New("native NVKE operator tooling supports Linux and macOS")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err := Info(); err != nil {
		return nil, 0, err
	}
	python, err := pythonRuntime(ctx, requestedPython)
	if ctx.Err() != nil {
		return nil, 0, errors.New("native tooling was interrupted; preserve the workspace and inspect its attempt record")
	}
	if err != nil {
		return nil, 0, err
	}
	directory, err := os.MkdirTemp("", "astro-nvke-runner-")
	if err != nil {
		return nil, 0, errors.New("could not create a private native runner directory")
	}
	defer func() { _ = os.RemoveAll(directory) }()
	filename := filepath.Join(directory, "tooling.py")
	if err := privatefile.Write(filename, runner); err != nil {
		return nil, 0, errors.New("could not prepare the private native runner")
	}
	command := exec.CommandContext(ctx, python, append([]string{"-I", filename}, args...)...)
	command.Env = nativeEnvironment()
	command.Stdin = input
	var output metadataBuffer
	command.Stdout, command.Stderr = &output, io.Discard
	configureProcess(command)
	err = command.Run()
	if ctx.Err() != nil {
		return nil, 0, errors.New("native tooling was interrupted; preserve the workspace and inspect its attempt record")
	}
	status := 0
	if err != nil {
		var failure *exec.ExitError
		if !errors.As(err, &failure) {
			return nil, 0, errors.New("native tooling could not be observed; inspect private artifacts before recovery")
		}
		status = failure.ExitCode()
	}
	return output.data.Bytes(), status, nil
}
