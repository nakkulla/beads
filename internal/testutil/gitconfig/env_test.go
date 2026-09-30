package gitconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolateEnv(t *testing.T) {
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte("[test]\nvalue = fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	inherited := map[string]string{
		"GIT_CONFIG_COUNT":      "1",
		"GIT_CONFIG_KEY_0":      "test.value",
		"GIT_CONFIG_VALUE_0":    "inline",
		"GIT_CONFIG_KEY_99":     "test.orphan",
		"GIT_CONFIG_VALUE_99":   "orphan",
		"GIT_CONFIG_PARAMETERS": "'test.value=parameter'",
	}
	for key, value := range inherited {
		t.Setenv(key, value)
	}

	readValue := func(t *testing.T) string {
		t.Helper()
		cmd := exec.Command("git", "config", "--get", "test.value")
		cmd.Dir = filepath.Dir(config)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git config: %v (%s)", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := readValue(t); got != "parameter" {
		t.Fatalf("inherited configuration = %q, want parameter", got)
	}

	t.Run("isolated fixture", func(t *testing.T) {
		IsolateEnv(t)
		for key := range inherited {
			if _, present := os.LookupEnv(key); present {
				t.Errorf("%s still present", key)
			}
		}
		if got := readValue(t); got != "fixture" {
			t.Fatalf("isolated configuration = %q, want fixture", got)
		}
	})

	for key, want := range inherited {
		if got, present := os.LookupEnv(key); !present || got != want {
			t.Errorf("%s not restored: got %q (present=%v), want %q", key, got, present, want)
		}
	}
}
