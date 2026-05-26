package review

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()

	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git rev-parse --show-toplevel unavailable: %v: %s", err, strings.TrimSpace(stderr.String()))
	}

	root := strings.TrimSpace(string(out))
	if root == "" {
		t.Skipf("git rev-parse --show-toplevel returned empty output")
	}
	return root
}

func TestStartScriptPreservesRotationEnv(t *testing.T) {
	root := repoRoot(t)
	contentBytes, err := os.ReadFile(filepath.Join(root, "scripts", "start.sh"))
	if err != nil {
		t.Fatalf("read scripts/start.sh: %v", err)
	}
	content := string(contentBytes)

	if !strings.Contains(content, "ASTOCK_ROTATION_ENABLED:=0") {
		t.Fatalf("scripts/start.sh must contain safe default/pass-through expansion ASTOCK_ROTATION_ENABLED:=0")
	}
	if !strings.Contains(content, "export ASTOCK_ROTATION_ENABLED") {
		t.Fatalf("scripts/start.sh must export ASTOCK_ROTATION_ENABLED")
	}
	if strings.Contains(content, "exec go run ./cmd/paper") {
		t.Fatalf("scripts/start.sh must not exec go run ./cmd/paper; it should start in the background")
	}
	if !strings.Contains(content, "nohup go run ./cmd/paper") {
		t.Fatalf("scripts/start.sh must use nohup go run ./cmd/paper")
	}
	if !strings.Contains(content, "scripts/pids/paper_trader.pid") {
		t.Fatalf("scripts/start.sh must write scripts/pids/paper_trader.pid")
	}
	if !strings.Contains(content, "logs") {
		t.Fatalf("scripts/start.sh must use logs")
	}
	if !strings.Contains(content, `echo "$pid" >"$PID_FILE"`) && !strings.Contains(content, `printf "%s\n" "$pid" >"$PID_FILE"`) {
		t.Fatalf("scripts/start.sh must write the captured background PID into $PID_FILE")
	}

	directAssignment := regexp.MustCompile(`(?m)^\s*ASTOCK_ROTATION_ENABLED=`)
	if match := directAssignment.FindString(content); match != "" {
		t.Fatalf("scripts/start.sh must not overwrite command-prefix ASTOCK_ROTATION_ENABLED value with direct assignment %q", match)
	}

	checkShellSyntax := func(script string) {
		cmd := exec.Command("bash", "-n", script)
		cmd.Dir = root
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("bash -n %s failed: %v\nstdout:\n%s\nstderr:\n%s", script, err, stdout.String(), stderr.String())
		}
	}

	checkShellSyntax(filepath.Join("scripts", "start.sh"))
	if _, err := os.Stat(filepath.Join(root, "scripts", "stop.sh")); err == nil {
		checkShellSyntax(filepath.Join("scripts", "stop.sh"))
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat scripts/stop.sh: %v", err)
	}
}

func TestRotationSwitchProtectedScope(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("git", "diff", "--name-only", "HEAD", "--")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git diff --name-only HEAD -- failed: %v: %s", err, strings.TrimSpace(stderr.String()))
	}

	allowed := map[string]bool{
		"cmd/paper/main.go":                  true,
		"cmd/paper/main_rotation_test.go":    true,
		"scripts/start.sh":                  true,
		"review/rotation_switch_scope_test.go": true,
	}

	for _, changedPath := range strings.Split(string(out), "\n") {
		changedPath = strings.TrimSpace(changedPath)
		if changedPath == "" {
			continue
		}
		if !allowed[changedPath] {
			t.Fatalf("unexpected changed path %q; this task must not modify protected trading modules such as engine, position, portfolio, risk, execctrl, executor, broker/paper, alpha, strategy, signal, decision/topn, rotation, market session definitions, or candidate-pool scoring/filtering", changedPath)
		}
	}
}
