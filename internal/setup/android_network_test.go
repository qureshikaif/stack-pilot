package setup

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// Downloads the real Android command-line tools (~150 MB) into a temp SDK and
// runs sdkmanager. Opt in with STACKPILOT_NETWORK_TESTS=1.
func TestInstallCmdlineToolsNetwork(t *testing.T) {
	if os.Getenv("STACKPILOT_NETWORK_TESTS") == "" {
		t.Skip("set STACKPILOT_NETWORK_TESTS=1 to run")
	}
	p := Platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Home: t.TempDir()}
	e := newEnv(context.Background(), p, Config{}, os.Stdout)
	e.AndroidHome = t.TempDir()

	if err := installCmdlineTools(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(e.sdkmanager(), "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("sdkmanager --version: %v\n%s", err, out)
	}
	t.Logf("sdkmanager %s", out)
}
