package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestProtectedCoreLogicDiffUnchanged(t *testing.T) {
	rootBytes, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel failed: %v", err)
	}
	root := strings.TrimSpace(string(rootBytes))
	protected := []string{
		"engine",
		"position",
		"portfolio",
		"risk",
		"execctrl",
		"executor",
		"broker/paper",
		"strategy",
		"signal",
		"decision/topn",
		"rotation",
	}
	args := append([]string{"-C", root, "diff", "--name-only", "HEAD", "--"}, protected...)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git diff protected paths failed: %v\n%s", err, string(out))
	}
	if changed := strings.TrimSpace(string(out)); changed != "" {
		t.Fatalf("protected core trading logic files changed unexpectedly:\n%s", changed)
	}
}
