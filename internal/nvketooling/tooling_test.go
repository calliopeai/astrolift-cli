package nvketooling

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
)

func TestInfoMatchesTheManagedCatalogAndCompleteNativeFamilies(t *testing.T) {
	info, err := Info()
	if err != nil || info.UpstreamRevision != nvkecatalog.Pinned().Revision || len(info.Commands) != 16 {
		t.Fatalf("invalid native tool provenance: %v", err)
	}
	wanted := map[string]bool{"bootstrap site": false, "management install": false, "management configure-access": false, "inspect nvkr-release": false, "render management-apis": false}
	for _, command := range info.Commands {
		if _, ok := wanted[command]; ok {
			wanted[command] = true
		}
		if command == "get kubeconfig" || command == "delete cluster" {
			t.Fatal("local runner bypasses a managed operation")
		}
	}
	for command, found := range wanted {
		if !found {
			t.Errorf("missing native tooling family %s", command)
		}
	}
}

func TestManagementAPIRendererDriftIsRefusedBeforeInvocation(t *testing.T) {
	original := managementAPIRenderer
	t.Cleanup(func() { managementAPIRenderer = original })
	managementAPIRenderer = append(append([]byte(nil), original...), []byte("\n# changed\n")...)
	if _, err := Info(); err == nil {
		t.Fatal("management API renderer drift was accepted")
	}
}

func TestMetadataBufferCannotBypassItsLimitThroughIOCopy(t *testing.T) {
	var output metadataBuffer
	_, err := io.Copy(&output, bytes.NewReader(bytes.Repeat([]byte("x"), 100000)))
	if err == nil || output.data.Len() > 64*1024 {
		t.Fatal("oversized native output bypassed the metadata limit")
	}
}

func nativeFixture(t *testing.T) (string, string, string) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native operator tools support Linux and macOS")
	}
	python, err := pythonRuntime(context.Background(), "")
	if err != nil {
		t.Skip("Python 3.12+ is required for native tooling execution tests")
	}
	bundle, workspace := t.TempDir(), t.TempDir()
	for _, directory := range []string{bundle, workspace} {
		if err := os.Chmod(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	binary := []byte("#!" + python + "\n" + `import json, os, sys, time
from pathlib import Path
if sys.argv[1:3] == ["bootstrap", "site"]:
    marker = Path("effect-count")
    marker.write_text(str(int(marker.read_text()) + 1) if marker.exists() else "1")
    if "slow.yaml" in sys.argv:
        Path("native-pid").write_text(str(os.getpid()))
        time.sleep(5)
    print("PRIVATE_NATIVE_FAILURE", file=sys.stderr)
    sys.exit(17)
print(json.dumps({"args": sys.argv[1:], "astroTokenInherited": "ASTROLIFT_TOKEN" in os.environ}))
`)
	if err := os.WriteFile(filepath.Join(bundle, "nvkectl"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	manifest, err := json.Marshal(map[string]any{"format": 1, "revision": nvkecatalog.Pinned().Revision, "binarySha256": hex.EncodeToString(digest[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := privatefile.Write(filepath.Join(bundle, "bundle.json"), manifest); err != nil {
		t.Fatal(err)
	}
	return python, bundle, workspace
}

func TestRunPreservesNativeArgumentsAndKeepsTokensAndOutputPrivate(t *testing.T) {
	python, bundle, workspace := nativeFixture(t)
	t.Setenv("ASTROLIFT_TOKEN", "PRIVATE_PLATFORM_TOKEN")
	args := []string{"generate", "sitespec", "native-test", "--nodes", "literal $(touch shell-expansion-marker).csv"}
	result, err := Run(context.Background(), python, bundle, workspace, args, nil)
	if err != nil || result.ExitCode != 0 || result.Phase != "completed" {
		t.Fatalf("native wrapper failed: %v", err)
	}
	data, err := os.ReadFile(result.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Arguments []string `json:"args"`
		Token     bool     `json:"astroTokenInherited"`
	}
	if json.Unmarshal(data, &native) != nil || native.Token || strings.Join(native.Arguments, "\x00") != strings.Join(args, "\x00") {
		t.Fatal("native arguments changed or platform credentials escaped their boundary")
	}
	encoded, err := json.Marshal(result)
	if err != nil || bytes.Contains(encoded, []byte("PRIVATE_PLATFORM_TOKEN")) || bytes.Contains(encoded, []byte("touch shell-expansion-marker")) {
		t.Fatal("native parameters or credentials escaped through run metadata")
	}
	if _, err := os.Stat(filepath.Join(workspace, "shell-expansion-marker")); !os.IsNotExist(err) {
		t.Fatal("native argument was evaluated by a shell")
	}
}

func TestFailedNativeAttemptRemainsRecoverableWithoutReexecution(t *testing.T) {
	python, bundle, workspace := nativeFixture(t)
	args := []string{"bootstrap", "site", "--sitespec", "test.yaml"}
	result, err := Run(context.Background(), python, bundle, workspace, args, nil)
	if err != nil || result.ExitCode != 17 || result.Phase != "uncertain" {
		t.Fatalf("failed native attempt was not retained: %v", err)
	}
	if _, err := Run(context.Background(), python, bundle, workspace, args, nil); err == nil || strings.Contains(err.Error(), "PRIVATE_NATIVE_FAILURE") {
		t.Fatal("native attempt was repeated or leaked its diagnostic payload")
	}
	count, err := os.ReadFile(filepath.Join(workspace, "effect-count"))
	if err != nil || string(count) != "1" {
		t.Fatal("native bootstrap effect was replayed")
	}
	if _, err := os.Stat(filepath.Join(workspace, "bootstrap-state.json")); err != nil {
		t.Fatal("native recovery record was removed")
	}
}

func TestInterruptedNativeProcessGroupRetainsItsAttempt(t *testing.T) {
	python, bundle, workspace := nativeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := Run(ctx, python, bundle, workspace, []string{"bootstrap", "site", "--sitespec", "slow.yaml"}, nil)
		finished <- err
	}()
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(workspace, "native-pid"))
		if err == nil {
			pid, _ = strconv.Atoi(string(data))
			if pid > 1 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid <= 1 {
		t.Fatal("native child never started")
	}
	cancel()
	if err := <-finished; err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatal("interrupted native execution was not reported conservatively")
	}
	deadline = time.Now().Add(2 * time.Second)
	stopped := false
	for time.Now().Before(deadline) {
		status, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
		if err != nil || strings.TrimSpace(string(status)) == "" || strings.HasPrefix(strings.TrimSpace(string(status)), "Z") {
			stopped = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !stopped {
		t.Fatal("native child continued running after cancellation")
	}
	if _, err := Run(context.Background(), python, bundle, workspace, []string{"bootstrap", "site", "--sitespec", "slow.yaml"}, nil); err == nil {
		t.Fatal("interruption allowed a second bootstrap attempt")
	}
}

func TestCancelledNativePreflightDoesNotConsumeAnAttempt(t *testing.T) {
	python, bundle, workspace := nativeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, python, bundle, workspace, []string{"bootstrap", "site", "--sitespec", "test.yaml"}, nil)
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatal("cancelled preflight was mistaken for a missing runtime")
	}
	if _, err := os.Stat(filepath.Join(workspace, "bootstrap-state.json")); !os.IsNotExist(err) {
		t.Fatal("cancelled preflight consumed an effectful attempt")
	}
}

func TestRealPreparedNativeBundle(t *testing.T) {
	bundle := os.Getenv("NVKE_TEST_NATIVE_BUNDLE")
	if bundle == "" {
		t.Skip("set NVKE_TEST_NATIVE_BUNDLE for real pinned native proof")
	}
	workspace := t.TempDir()
	if err := os.Chmod(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"version"}, {"get", "bootstrap-manifest"}} {
		result, err := Run(context.Background(), "", bundle, workspace, args, nil)
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("real native command failed: %v", err)
		}
	}
}
