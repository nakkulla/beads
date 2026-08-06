package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedBeadsConfigYaml writes content to <tmp>/.beads/config.yaml and returns
// the beads dir.
func seedBeadsConfigYaml(t *testing.T, content string) string {
	t.Helper()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return beadsDir
}

func readBeadsConfigYaml(t *testing.T, beadsDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(beadsDir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// hasActiveKeyLine reports whether any uncommented line declares key.
func hasActiveKeyLine(content, key string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, key+":") {
			return true
		}
	}
	return false
}

func TestUnsetYamlConfigInDir_NestedShape(t *testing.T) {
	beadsDir := seedBeadsConfigYaml(t, `# Beads Configuration File
# keep this comment

sync:
  remote: "git+https://github.com/org/repo.git"
  branch: main

backup:
  enabled: false
`)

	if got := GetStringFromDir(beadsDir, "sync.remote"); got == "" {
		t.Fatalf("precondition: sync.remote should be readable, got %q", got)
	}

	if err := UnsetYamlConfigInDir(beadsDir, "sync.remote"); err != nil {
		t.Fatalf("UnsetYamlConfigInDir: %v", err)
	}

	if got := GetStringFromDir(beadsDir, "sync.remote"); got != "" {
		t.Errorf("sync.remote = %q after unset, want empty", got)
	}

	content := readBeadsConfigYaml(t, beadsDir)
	// Adjacent keys and comments survive.
	if got := GetStringFromDir(beadsDir, "sync.branch"); got != "main" {
		t.Errorf("sync.branch = %q, want %q preserved", got, "main")
	}
	if got := GetStringFromDir(beadsDir, "backup.enabled"); got != "false" {
		t.Errorf("backup.enabled = %q, want %q preserved", got, "false")
	}
	if !strings.Contains(content, "# keep this comment") {
		t.Errorf("comment was dropped:\n%s", content)
	}
	if !strings.Contains(content, "# Beads Configuration File") {
		t.Errorf("header comment was dropped:\n%s", content)
	}
}

func TestUnsetYamlConfigInDir_FlatRootKeyShape(t *testing.T) {
	// bd init renders an all-comment config.yaml, so SetYamlConfigInDir writes
	// the flat root key form rather than the nested mapping.
	beadsDir := seedBeadsConfigYaml(t, `# Beads Configuration File
# keep this comment

backup.enabled: false
sync.remote: "https://github.com/org/repo.git"
`)

	if err := UnsetYamlConfigInDir(beadsDir, "sync.remote"); err != nil {
		t.Fatalf("UnsetYamlConfigInDir: %v", err)
	}

	content := readBeadsConfigYaml(t, beadsDir)
	if hasActiveKeyLine(content, "sync.remote") {
		t.Errorf("flat sync.remote key still active:\n%s", content)
	}
	if !hasActiveKeyLine(content, "backup.enabled") {
		t.Errorf("adjacent flat key backup.enabled was removed:\n%s", content)
	}
	if !strings.Contains(content, "# keep this comment") {
		t.Errorf("comment was dropped:\n%s", content)
	}
}

func TestUnsetYamlConfigInDir_AbsentKeyIsNoOp(t *testing.T) {
	original := `# Beads Configuration File

backup.enabled: false
`
	beadsDir := seedBeadsConfigYaml(t, original)

	if err := UnsetYamlConfigInDir(beadsDir, "sync.remote"); err != nil {
		t.Fatalf("UnsetYamlConfigInDir on absent key: %v", err)
	}

	if got := readBeadsConfigYaml(t, beadsDir); got != original {
		t.Errorf("content changed for an absent key:\ngot:\n%s\nwant:\n%s", got, original)
	}
}

func TestUnsetYamlConfigInDir_MissingFileIsNoOp(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := UnsetYamlConfigInDir(beadsDir, "sync.remote"); err != nil {
		t.Fatalf("UnsetYamlConfigInDir with no config.yaml: %v", err)
	}

	if _, err := os.Stat(filepath.Join(beadsDir, "config.yaml")); !os.IsNotExist(err) {
		t.Errorf("config.yaml was created by unset (stat err = %v)", err)
	}
}

// A deeper key that ends in the same leaf name (sync.mirror.remote) is a
// different key and must survive.
func TestUnsetYamlConfigInDir_DeeperSameLeafIsNotTouched(t *testing.T) {
	beadsDir := seedBeadsConfigYaml(t, `sync:
  mirror:
    remote: "git+https://github.com/org/mirror.git"
  branch: main
`)

	if err := UnsetYamlConfigInDir(beadsDir, "sync.remote"); err != nil {
		t.Fatalf("UnsetYamlConfigInDir: %v", err)
	}

	if got := GetStringFromDir(beadsDir, "sync.mirror.remote"); got != "git+https://github.com/org/mirror.git" {
		t.Errorf("sync.mirror.remote = %q, want it preserved", got)
	}
	if got := GetStringFromDir(beadsDir, "sync.branch"); got != "main" {
		t.Errorf("sync.branch = %q, want %q preserved", got, "main")
	}
}

func TestUnsetYamlConfigInDir_BothShapesInOneFile(t *testing.T) {
	beadsDir := seedBeadsConfigYaml(t, `sync:
  remote: "git+https://github.com/org/repo.git"
  branch: main
sync.remote: "https://github.com/org/repo.git"
`)

	if err := UnsetYamlConfigInDir(beadsDir, "sync.remote"); err != nil {
		t.Fatalf("UnsetYamlConfigInDir: %v", err)
	}

	content := readBeadsConfigYaml(t, beadsDir)
	if hasActiveKeyLine(content, "sync.remote") {
		t.Errorf("flat sync.remote key still active:\n%s", content)
	}
	if got := GetStringFromDir(beadsDir, "sync.remote"); got != "" {
		t.Errorf("nested sync.remote = %q after unset, want empty", got)
	}
	if got := GetStringFromDir(beadsDir, "sync.branch"); got != "main" {
		t.Errorf("sync.branch = %q, want %q preserved", got, "main")
	}
}
