package review

import (
	"os/exec"
	"strings"
	"testing"
)

func TestNoProtectedCoreTradingFilesChanged(t *testing.T) {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status failed: %v\n%s", err, out)
	}

	protectedPrefixes := []string{
		"engine/",
		"position/",
		"portfolio/",
		"risk/",
		"execctrl/",
		"executor/",
		"broker/paper/",
		"strategy/",
		"signal/",
		"decision/topn/",
		"rotation/",
	}

	var violations []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) < 4 {
			continue
		}

		path := strings.TrimSpace(line[3:])
		if strings.Contains(path, " -> ") {
			parts := strings.Split(path, " -> ")
			path = strings.TrimSpace(parts[len(parts)-1])
		}

		for _, prefix := range protectedPrefixes {
			if strings.HasPrefix(path, prefix) {
				violations = append(violations, path)
				break
			}
		}
	}

	if len(violations) > 0 {
		t.Fatalf("protected core trading files changed: %s", strings.Join(violations, ", "))
	}
}
