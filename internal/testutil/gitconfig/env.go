package gitconfig

import (
	"os"
	"strings"
	"testing"
)

// IsolateEnv removes command-scope Git config for the duration of a
// test so inherited git -c settings cannot override its fixture configuration.
func IsolateEnv(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "GIT_CONFIG_COUNT" || key == "GIT_CONFIG_PARAMETERS" ||
			strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			// Setenv registers restoration of the original value at test cleanup.
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("unset %s: %v", key, err)
			}
		}
	}
}
