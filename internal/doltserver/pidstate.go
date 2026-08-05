package doltserver

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/steveyegge/beads/internal/lockfile"
)

// PIDStateStatus classifies the on-disk dolt server PID state.
type PIDStateStatus string

const (
	// PIDStateAbsent means no dolt-server.pid file exists.
	PIDStateAbsent PIDStateStatus = "absent"
	// PIDStateLive means the recorded PID is a running dolt sql-server.
	PIDStateLive PIDStateStatus = "live"
	// PIDStateCorrupt means dolt-server.pid exists but holds no usable PID.
	PIDStateCorrupt PIDStateStatus = "corrupt"
	// PIDStateDead means the recorded PID is not running.
	PIDStateDead PIDStateStatus = "dead"
	// PIDStateReused means the recorded PID is alive but is not a dolt server.
	PIDStateReused PIDStateStatus = "reused"
)

// PIDState is a read-only snapshot of the dolt server lifecycle files
// (dolt-server.pid / dolt-server.port) plus the liveness verdict for the
// recorded PID.
//
// Unlike IsRunning, inspecting never mutates the .beads directory, so callers
// such as `bd doctor` can report the state before deciding to repair it.
type PIDState struct {
	BeadsDir string `json:"beads_dir"`
	PIDPath  string `json:"pid_path"`
	PortPath string `json:"port_path"`

	PIDFileExists  bool `json:"pid_file_exists"`
	PortFileExists bool `json:"port_file_exists"`

	// Raw trimmed file contents, retained so a corrupt file can be reported
	// verbatim instead of as a bare parse failure.
	PIDFileRaw  string `json:"pid_file_raw,omitempty"`
	PortFileRaw string `json:"port_file_raw,omitempty"`

	// RecordedPID / RecordedPort are 0 when the file is absent or unparseable.
	RecordedPID  int `json:"recorded_pid"`
	RecordedPort int `json:"recorded_port"`

	ProcessAlive bool `json:"process_alive"`
	IsDoltServer bool `json:"is_dolt_server"`

	Status PIDStateStatus `json:"status"`
	Stale  bool           `json:"stale"`
	Reason string         `json:"reason"`
}

// PIDStateCleanupResult reports what CleanupStalePIDState did (or why it did
// nothing).
type PIDStateCleanupResult struct {
	// Before is the pre-lock inspection that motivated the repair attempt.
	Before PIDState `json:"before"`
	// Verified is the inspection re-taken while holding the start lock. It is
	// the zero value when the lock was never acquired.
	Verified PIDState `json:"verified"`

	LockAcquired bool     `json:"lock_acquired"`
	Cleaned      bool     `json:"cleaned"`
	Removed      []string `json:"removed,omitempty"`
	Reason       string   `json:"reason"`
}

// maxRawStateLen bounds how much of a corrupt state file is echoed back.
const maxRawStateLen = 64

// InspectPIDState reports the dolt server PID/port file state for beadsDir
// without modifying anything. Missing and corrupt files are states, not
// errors, so the caller always gets a complete verdict.
func InspectPIDState(beadsDir string) PIDState {
	st := PIDState{
		BeadsDir: beadsDir,
		PIDPath:  pidPath(beadsDir),
		PortPath: portPath(beadsDir),
		Status:   PIDStateAbsent,
	}

	if data, err := os.ReadFile(st.PortPath); err == nil {
		st.PortFileExists = true
		st.PortFileRaw = truncateRawState(strings.TrimSpace(string(data)))
		if port, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && port > 0 {
			st.RecordedPort = port
		}
	}

	data, err := os.ReadFile(st.PIDPath)
	if err != nil {
		// No PID file: beads owns no server process here. An orphan port file
		// is not treated as stale — externally managed rigs legitimately have
		// a port file (EnsurePortFile) and no PID file.
		if st.PortFileExists {
			st.Reason = fmt.Sprintf("no %s (only %s is present)", PIDFileName, PortFileName)
		} else {
			st.Reason = fmt.Sprintf("no %s", PIDFileName)
		}
		return st
	}

	st.PIDFileExists = true
	trimmed := strings.TrimSpace(string(data))
	st.PIDFileRaw = truncateRawState(trimmed)

	pid, convErr := strconv.Atoi(trimmed)
	if convErr != nil || pid <= 0 {
		st.Status = PIDStateCorrupt
		st.Stale = true
		st.Reason = fmt.Sprintf("%s does not contain a valid PID (%q)", PIDFileName, st.PIDFileRaw)
		return st
	}
	st.RecordedPID = pid

	if !isProcessAlive(pid) {
		st.Status = PIDStateDead
		st.Stale = true
		st.Reason = fmt.Sprintf("recorded PID %d is not running", pid)
		return st
	}
	st.ProcessAlive = true

	if !isDoltProcess(pid) {
		st.Status = PIDStateReused
		st.Stale = true
		st.Reason = fmt.Sprintf("PID %d is running but is not a dolt sql-server (PID reuse)", pid)
		return st
	}
	st.IsDoltServer = true

	st.Status = PIDStateLive
	st.Reason = fmt.Sprintf("PID %d is a running dolt sql-server", pid)
	return st
}

func truncateRawState(s string) string {
	if len(s) <= maxRawStateLen {
		return s
	}
	return s[:maxRawStateLen] + "…"
}

// CleanupStalePIDState removes the dolt-server.pid / dolt-server.port files
// left behind by a server process that is gone, and nothing else.
//
// Safety properties:
//   - dolt-server.lock is never removed. It is not a PID-owned lock but the
//     flock anchor Start() uses to serialize concurrent starts; deleting it
//     would let a competing start lock a fresh inode while another process
//     holds the old one, breaking mutual exclusion.
//   - The lock is taken with TryLock. A held lock means a start is in
//     progress (its PID file may not be written yet), so the repair reports
//     that and stops instead of blocking a live rig's `bd doctor` run.
//   - The PID is re-read and re-verified while holding the lock, so a server
//     that started between the diagnosis and the repair is preserved.
func CleanupStalePIDState(beadsDir string) (PIDStateCleanupResult, error) {
	return cleanupStalePIDState(beadsDir, nil)
}

// cleanupStalePIDState is the testable core. underLock, when non-nil, runs
// immediately after the start lock is acquired and before the state is
// re-verified — it exists so tests can drive the TOCTOU window deterministically.
func cleanupStalePIDState(beadsDir string, underLock func()) (PIDStateCleanupResult, error) {
	res := PIDStateCleanupResult{Before: InspectPIDState(beadsDir)}

	if !res.Before.Stale {
		// Nothing to repair. Return before touching the lock file so a healthy
		// rig never gains state files as a side effect of running doctor.
		res.Reason = fmt.Sprintf("no stale server state to clean (%s)", res.Before.Reason)
		return res, nil
	}

	lockF, err := os.OpenFile(lockPath(beadsDir), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return res, fmt.Errorf("opening server start lock: %w", err)
	}
	defer func() { _ = lockF.Close() }()

	if err := lockfile.FlockExclusiveNonBlocking(lockF); err != nil {
		if lockfile.IsLocked(err) {
			res.Reason = "another process holds the dolt server start lock (a server start is in progress); left all state files untouched"
			return res, nil
		}
		return res, fmt.Errorf("acquiring server start lock: %w", err)
	}
	res.LockAcquired = true
	defer func() { _ = lockfile.FlockUnlock(lockF) }()

	if underLock != nil {
		underLock()
	}

	// Re-verify process identity inside the lock: the pre-lock verdict may be
	// stale if a server started (or another repair ran) in the meantime.
	res.Verified = InspectPIDState(beadsDir)
	if !res.Verified.Stale {
		res.Reason = fmt.Sprintf("server state changed after acquiring the start lock (%s); nothing removed", res.Verified.Reason)
		return res, nil
	}

	var removed []string
	for _, path := range []string{pidPath(beadsDir), portPath(beadsDir)} {
		if _, statErr := os.Stat(path); statErr != nil {
			continue
		}
		if rmErr := os.Remove(path); rmErr != nil {
			res.Removed = removed
			res.Reason = "partial cleanup"
			return res, fmt.Errorf("removing %s: %w", path, rmErr)
		}
		removed = append(removed, path)
	}

	res.Removed = removed
	res.Cleaned = len(removed) > 0
	if res.Cleaned {
		res.Reason = fmt.Sprintf("removed stale server state (%s)", res.Verified.Reason)
	} else {
		res.Reason = "no state files to remove"
	}
	return res, nil
}
