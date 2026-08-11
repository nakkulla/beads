package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoOpsDeclaresManagedDeployAdapter(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(sourceRepoRoot(t), "docs", "agents", "repo-ops.toml"))
	if err != nil {
		t.Fatal(err)
	}
	deploy := deploySection(t, string(data))
	for _, want := range []string{
		"adapter = \"managed\"",
		"cmd = [\"scripts/bdui-managed-deploy.sh\"]",
		"timeout_ms = 600000",
	} {
		if !strings.Contains(deploy, want) {
			t.Fatalf("[deploy] is missing %q:\n%s", want, deploy)
		}
	}
	if strings.Contains(deploy, "detached") {
		t.Fatalf("[deploy] must not declare detached:\n%s", deploy)
	}
}

func deploySection(t *testing.T, toml string) string {
	t.Helper()
	start := strings.Index(toml, "[deploy]\n")
	if start < 0 {
		t.Fatal("[deploy] section is missing")
	}
	section := toml[start:]
	if next := strings.Index(section[len("[deploy]\n"):], "\n["); next >= 0 {
		section = section[:len("[deploy]\n")+next]
	}
	return section
}
