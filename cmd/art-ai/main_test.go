package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `art-ai diagnose` (#234) is dispatched at the very top of main: before the
// log file is opened for writing, before any configuration is validated and
// before the service is assembled. The test runs main itself in a child process
// (the test binary, re-executed) and checks what the child leaves behind.
func TestDiagnoseSubcommandNeverReachesTheServerPath(t *testing.T) {
	if os.Getenv("ART_AI_TEST_CHILD") == "1" {
		os.Args = append([]string{"art-ai"}, strings.Split(os.Getenv("ART_AI_TEST_ARGS"), "|")...)
		main() // exits the process
		return
	}
	directory := t.TempDir()
	logs := filepath.Join(directory, "logs")
	if err := os.Mkdir(logs, 0o750); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(directory, "bundle.tar.gz")
	command := exec.Command(os.Args[0], "-test.run=^TestDiagnoseSubcommandNeverReachesTheServerPath$")
	command.Env = append(os.Environ(), "ART_AI_TEST_CHILD=1", "ART_AI_TEST_ARGS=diagnose|--out|"+out,
		"LOG_DIR="+logs, "DATABASE_PATH="+filepath.Join(directory, "missing.db"), "APP_ENV=test",
		// A setting that would stop the service from starting: diagnose does not care.
		"PROVIDER_MODE=not-a-mode")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("art-ai diagnose failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "诊断包已写出") {
		t.Fatalf("no summary in the output:\n%s", output)
	}
	if info, err := os.Stat(out); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("the bundle: %v %v", info, err)
	}
	// The server path would have opened art-ai.log for writing in the log directory.
	if entries, err := os.ReadDir(logs); err != nil || len(entries) != 0 {
		t.Fatalf("the log directory was touched: %v %v", entries, err)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 2 {
		t.Fatalf("only the log directory and the bundle may exist: %v %v", entries, err)
	}
}
