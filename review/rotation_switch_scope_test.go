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
	if !strings.Contains(content, "exec go run ./cmd/paper") {
		t.Fatalf("scripts/start.sh must exec go run ./cmd/paper")
	}

	directAssignment := regexp.MustCompile(`(?m)^\s*ASTOCK_ROTATION_ENABLED=`)
	if match := directAssignment.FindString(content); match != "" {
		t.Fatalf("scripts/start.sh must not overwrite command-prefix ASTOCK_ROTATION_ENABLED value with direct assignment %q", match)
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
