package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMakeInstallChecksForUpdates(t *testing.T) {
	repo, home, checkMarker := installFixture(t)
	runMakeInstall(t, repo, home, checkMarker, "install")
	if _, err := os.Stat(checkMarker); err != nil {
		t.Fatalf("make install did not invoke check-up-to-date: %v", err)
	}
	assertInstalledAlias(t, home)
}

func TestMakeInstallForceSkipsUpdateCheck(t *testing.T) {
	repo, home, checkMarker := installFixture(t)
	runMakeInstall(t, repo, home, checkMarker, "install-force")
	if _, err := os.Stat(checkMarker); !os.IsNotExist(err) {
		t.Fatalf("make install-force unexpectedly invoked check-up-to-date: %v", err)
	}
	assertInstalledAlias(t, home)
}

func installFixture(t *testing.T) (repo, home, checkMarker string) {
	t.Helper()

	repo = t.TempDir()
	makefile, err := os.ReadFile(filepath.Join(sourceRepoRoot(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), makefile, 0o644); err != nil {
		t.Fatal(err)
	}
	override := "build:\n\t@printf '#!/bin/sh\\nexit 0\\n' > ./bd\n\t@chmod +x ./bd\n\ncheck-up-to-date:\n\t@touch \"$$INSTALL_TARGETS_CHECK_MARKER\"\n"
	if err := os.WriteFile(filepath.Join(repo, "test-override.mk"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	home = t.TempDir()
	checkMarker = filepath.Join(t.TempDir(), "check-up-to-date")
	return repo, home, checkMarker
}

func runMakeInstall(t *testing.T, repo, home, checkMarker, target string) {
	t.Helper()
	cmd := exec.Command("make", "-f", "Makefile", "-f", "test-override.mk", target)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"INSTALL_TARGETS_CHECK_MARKER="+checkMarker,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make %s: %v\n%s", target, err, out)
	}
}

func assertInstalledAlias(t *testing.T, home string) {
	t.Helper()
	bd := filepath.Join(home, ".local", "bin", "bd")
	info, err := os.Stat(bd)
	if err != nil {
		t.Fatalf("installed bd: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatalf("installed bd is not executable regular file: %s", info.Mode())
	}
	alias := filepath.Join(home, ".local", "bin", "beads")
	target, err := os.Readlink(alias)
	if err != nil {
		t.Fatalf("read beads alias: %v", err)
	}
	if target != "bd" {
		t.Fatalf("beads alias = %q, want bd", target)
	}
}
