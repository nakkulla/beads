package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/doltserver"
)

// fabricateDeadPID returns a PID that is guaranteed not to be running: a
// short-lived child this process already reaped.
func fabricateDeadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot spawn helper process: %v", err)
	}
	return cmd.Process.Pid
}

func seedServerPIDState(t *testing.T, beadsDir, pid, port string) {
	t.Helper()
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if pid != "" {
		if err := os.WriteFile(filepath.Join(beadsDir, doltserver.PIDFileName), []byte(pid), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if port != "" {
		if err := os.WriteFile(filepath.Join(beadsDir, doltserver.PortFileName), []byte(port), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "dolt-server.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckStaleServerPIDState(t *testing.T) {
	t.Run("no beads dir", func(t *testing.T) {
		check := CheckStaleServerPIDState(t.TempDir())
		if check.Name != StaleServerPIDStateCheckName {
			t.Errorf("Name = %q, want %q", check.Name, StaleServerPIDStateCheckName)
		}
		if check.Status != StatusOK {
			t.Errorf("Status = %q, want %q (%s)", check.Status, StatusOK, check.Message)
		}
	})

	t.Run("no state files", func(t *testing.T) {
		tmpDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(tmpDir, ".beads"), 0o755); err != nil {
			t.Fatal(err)
		}
		check := CheckStaleServerPIDState(tmpDir)
		if check.Status != StatusOK {
			t.Errorf("Status = %q, want %q (%s)", check.Status, StatusOK, check.Message)
		}
		if check.Fix != "" {
			t.Errorf("Fix = %q, want empty for a clean rig", check.Fix)
		}
	})

	t.Run("dead pid is flagged and fixable", func(t *testing.T) {
		tmpDir := t.TempDir()
		pid := fabricateDeadPID(t)
		seedServerPIDState(t, filepath.Join(tmpDir, ".beads"), strconv.Itoa(pid), "13307")

		check := CheckStaleServerPIDState(tmpDir)

		if check.Status != StatusWarning {
			t.Errorf("Status = %q, want %q (%s)", check.Status, StatusWarning, check.Message)
		}
		if !strings.Contains(check.Detail, strconv.Itoa(pid)) {
			t.Errorf("Detail = %q, want it to name PID %d", check.Detail, pid)
		}
		if check.Fix == "" {
			t.Errorf("Fix is empty; stale PID state must be auto-fixable")
		}
	})

	t.Run("alive non-dolt pid is flagged", func(t *testing.T) {
		tmpDir := t.TempDir()
		seedServerPIDState(t, filepath.Join(tmpDir, ".beads"), strconv.Itoa(os.Getpid()), "13307")

		check := CheckStaleServerPIDState(tmpDir)

		if check.Status != StatusWarning {
			t.Errorf("Status = %q, want %q (%s)", check.Status, StatusWarning, check.Message)
		}
	})

	t.Run("live dolt server is preserved", func(t *testing.T) {
		hostPID := hostDoltPID(t)
		if hostPID == 0 {
			t.Skip("no dolt sql-server running on this host")
		}
		tmpDir := t.TempDir()
		seedServerPIDState(t, filepath.Join(tmpDir, ".beads"), strconv.Itoa(hostPID), "13307")

		check := CheckStaleServerPIDState(tmpDir)

		if check.Status != StatusOK {
			t.Errorf("Status = %q, want %q for a live server (%s / %s)", check.Status, StatusOK, check.Message, check.Detail)
		}
		if check.Fix != "" {
			t.Errorf("Fix = %q, want empty: a live server must not be offered for cleanup", check.Fix)
		}
	})

	t.Run("check never mutates state", func(t *testing.T) {
		tmpDir := t.TempDir()
		beadsDir := filepath.Join(tmpDir, ".beads")
		seedServerPIDState(t, beadsDir, strconv.Itoa(fabricateDeadPID(t)), "13307")

		_ = CheckStaleServerPIDState(tmpDir)

		for _, name := range []string{doltserver.PIDFileName, doltserver.PortFileName, "dolt-server.lock"} {
			if _, err := os.Stat(filepath.Join(beadsDir, name)); err != nil {
				t.Errorf("diagnostic removed %s: %v", name, err)
			}
		}
	})
}

// hostDoltPID returns a dolt sql-server PID running on this host, or 0.
func hostDoltPID(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("pgrep", "-f", "dolt.*sql-server").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, convErr := strconv.Atoi(strings.TrimSpace(line))
		if convErr != nil || pid <= 0 {
			continue
		}
		cmdOut, cmdErr := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
		if cmdErr != nil {
			continue
		}
		cmdline := strings.TrimSpace(string(cmdOut))
		if strings.Contains(cmdline, "dolt") && strings.Contains(cmdline, "sql-server") {
			return pid
		}
	}
	return 0
}
