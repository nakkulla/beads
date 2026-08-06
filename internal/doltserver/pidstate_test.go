package doltserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/lockfile"
)

// deadPID returns a PID that is guaranteed not to belong to a running process:
// a short-lived child that this process has already reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot spawn helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if isProcessAlive(pid) {
		t.Skipf("reaped helper PID %d still reports alive; cannot fabricate a dead PID", pid)
	}
	return pid
}

// liveDoltPID returns the PID of a dolt sql-server running on this host, or 0.
func liveDoltPID() int {
	pids := listDoltProcessPIDs()
	if len(pids) == 0 {
		return 0
	}
	return pids[0]
}

// writePIDState seeds .beads with the given pid/port files and the lock anchor.
func writePIDState(t *testing.T, beadsDir string, pid string, port string) {
	t.Helper()
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if pid != "" {
		if err := os.WriteFile(filepath.Join(beadsDir, PIDFileName), []byte(pid), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if port != "" {
		if err := os.WriteFile(filepath.Join(beadsDir, PortFileName), []byte(port), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The lock file is the Start() serialization anchor; production always
	// creates it with O_CREATE. Seed it so tests can assert it survives.
	if err := os.WriteFile(filepath.Join(beadsDir, "dolt-server.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

func TestInspectPIDState_NoFiles(t *testing.T) {
	beadsDir := t.TempDir()

	st := InspectPIDState(beadsDir)

	if st.PIDFileExists {
		t.Errorf("PIDFileExists = true, want false")
	}
	if st.Status != PIDStateAbsent {
		t.Errorf("Status = %q, want %q", st.Status, PIDStateAbsent)
	}
	if st.Stale {
		t.Errorf("Stale = true, want false for an empty dir")
	}
	if st.PIDPath != filepath.Join(beadsDir, PIDFileName) {
		t.Errorf("PIDPath = %q, want %q", st.PIDPath, filepath.Join(beadsDir, PIDFileName))
	}
}

func TestInspectPIDState_DeadPID(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	pid := deadPID(t)
	writePIDState(t, beadsDir, strconv.Itoa(pid), "13307")

	st := InspectPIDState(beadsDir)

	if st.Status != PIDStateDead {
		t.Errorf("Status = %q, want %q (reason: %s)", st.Status, PIDStateDead, st.Reason)
	}
	if !st.Stale {
		t.Errorf("Stale = false, want true for a dead PID")
	}
	if st.RecordedPID != pid {
		t.Errorf("RecordedPID = %d, want %d", st.RecordedPID, pid)
	}
	if st.RecordedPort != 13307 {
		t.Errorf("RecordedPort = %d, want 13307", st.RecordedPort)
	}
	if st.ProcessAlive {
		t.Errorf("ProcessAlive = true, want false")
	}
	if st.Reason == "" {
		t.Errorf("Reason is empty; the doctor check needs a rationale")
	}
	// Read-only: inspection must not delete anything (unlike IsRunning).
	if !fileExists(t, st.PIDPath) || !fileExists(t, st.PortPath) {
		t.Errorf("InspectPIDState removed state files; it must be read-only")
	}
}

func TestInspectPIDState_AliveNonDoltPID(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	// The test binary itself: alive, but not a dolt sql-server.
	writePIDState(t, beadsDir, strconv.Itoa(os.Getpid()), "13307")

	st := InspectPIDState(beadsDir)

	if st.Status != PIDStateReused {
		t.Errorf("Status = %q, want %q (reason: %s)", st.Status, PIDStateReused, st.Reason)
	}
	if !st.Stale {
		t.Errorf("Stale = false, want true for a reused PID")
	}
	if !st.ProcessAlive {
		t.Errorf("ProcessAlive = false, want true")
	}
	if st.IsDoltServer {
		t.Errorf("IsDoltServer = true, want false for the test binary")
	}
}

func TestInspectPIDState_LiveDoltPID(t *testing.T) {
	pid := liveDoltPID()
	if pid == 0 {
		t.Skip("no dolt sql-server running on this host")
	}
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	writePIDState(t, beadsDir, strconv.Itoa(pid), "13307")

	st := InspectPIDState(beadsDir)

	if st.Status != PIDStateLive {
		t.Errorf("Status = %q, want %q (reason: %s)", st.Status, PIDStateLive, st.Reason)
	}
	if st.Stale {
		t.Errorf("Stale = true, want false for a live dolt server")
	}
}

func TestInspectPIDState_CorruptPIDFile(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	writePIDState(t, beadsDir, "not-a-pid", "13307")

	st := InspectPIDState(beadsDir)

	if st.Status != PIDStateCorrupt {
		t.Errorf("Status = %q, want %q", st.Status, PIDStateCorrupt)
	}
	if !st.Stale {
		t.Errorf("Stale = false, want true for a corrupt PID file")
	}
}

func TestInspectPIDState_PortFileOnlyIsNotStale(t *testing.T) {
	// An externally managed server has no beads-owned PID file, but
	// EnsurePortFile may still have written a port file. That is not stale.
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	writePIDState(t, beadsDir, "", "13307")

	st := InspectPIDState(beadsDir)

	if st.Stale {
		t.Errorf("Stale = true, want false when only the port file exists")
	}
	if !st.PortFileExists || st.RecordedPort != 13307 {
		t.Errorf("port file not reported: exists=%v port=%d", st.PortFileExists, st.RecordedPort)
	}
}

func TestCleanupStalePIDState_RemovesDeadState(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	writePIDState(t, beadsDir, strconv.Itoa(deadPID(t)), "13307")
	lockFile := filepath.Join(beadsDir, "dolt-server.lock")
	lockBefore, err := os.Stat(lockFile)
	if err != nil {
		t.Fatal(err)
	}

	res, err := CleanupStalePIDState(beadsDir)
	if err != nil {
		t.Fatalf("CleanupStalePIDState: %v", err)
	}

	if !res.LockAcquired {
		t.Errorf("LockAcquired = false, want true on an idle rig")
	}
	if !res.Cleaned {
		t.Errorf("Cleaned = false, want true (reason: %s)", res.Reason)
	}
	if fileExists(t, filepath.Join(beadsDir, PIDFileName)) {
		t.Errorf("dolt-server.pid still present after cleanup")
	}
	if fileExists(t, filepath.Join(beadsDir, PortFileName)) {
		t.Errorf("dolt-server.port still present after cleanup")
	}
	// The lock file is the Start() serialization anchor: it must survive,
	// and it must be the SAME inode so a concurrent Start() blocked on it
	// still serializes against later starts.
	lockAfter, err := os.Stat(lockFile)
	if err != nil {
		t.Fatalf("dolt-server.lock was removed by cleanup: %v", err)
	}
	if !os.SameFile(lockBefore, lockAfter) {
		t.Errorf("dolt-server.lock inode changed; Start() serialization would break")
	}
}

func TestCleanupStalePIDState_PreservesLiveServer(t *testing.T) {
	pid := liveDoltPID()
	if pid == 0 {
		t.Skip("no dolt sql-server running on this host")
	}
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	writePIDState(t, beadsDir, strconv.Itoa(pid), "13307")

	res, err := CleanupStalePIDState(beadsDir)
	if err != nil {
		t.Fatalf("CleanupStalePIDState: %v", err)
	}

	if res.Cleaned {
		t.Errorf("Cleaned = true, want false for a live dolt server")
	}
	if !fileExists(t, filepath.Join(beadsDir, PIDFileName)) {
		t.Errorf("cleanup removed the PID file of a live server")
	}
	if !fileExists(t, filepath.Join(beadsDir, PortFileName)) {
		t.Errorf("cleanup removed the port file of a live server")
	}
}

func TestCleanupStalePIDState_NoOpWithoutPIDFile(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := CleanupStalePIDState(beadsDir)
	if err != nil {
		t.Fatalf("CleanupStalePIDState: %v", err)
	}
	if res.Cleaned {
		t.Errorf("Cleaned = true, want false with no state files")
	}
	// Nothing to clean must not create the lock anchor as a side effect.
	if fileExists(t, filepath.Join(beadsDir, "dolt-server.lock")) {
		t.Errorf("cleanup created dolt-server.lock with nothing to clean")
	}
}

// TestCleanupStalePIDState_SkipsWhenStartLockHeld is the concurrent-start
// regression: while another process holds the Start() flock, cleanup must not
// touch any state file (that process may be mid-start with its PID file not
// yet written), and it must not block waiting for the lock.
func TestCleanupStalePIDState_SkipsWhenStartLockHeld(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	writePIDState(t, beadsDir, strconv.Itoa(deadPID(t)), "13307")
	lockFile := filepath.Join(beadsDir, "dolt-server.lock")

	// Take the same lock Start() takes, the same way Start() takes it.
	holder, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if err := lockfile.FlockExclusiveNonBlocking(holder); err != nil {
		t.Fatalf("test could not take the start lock: %v", err)
	}

	done := make(chan struct{})
	var res PIDStateCleanupResult
	var cleanupErr error
	go func() {
		defer close(done)
		res, cleanupErr = CleanupStalePIDState(beadsDir)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("CleanupStalePIDState blocked on the start lock; it must use TryLock")
	}
	_ = lockfile.FlockUnlock(holder)

	if cleanupErr != nil {
		t.Fatalf("CleanupStalePIDState: %v", cleanupErr)
	}
	if res.LockAcquired {
		t.Errorf("LockAcquired = true while another process held the lock")
	}
	if res.Cleaned {
		t.Errorf("Cleaned = true while a start was in progress")
	}
	if res.Reason == "" {
		t.Errorf("Reason is empty; the skip must be reported")
	}
	if !fileExists(t, filepath.Join(beadsDir, PIDFileName)) || !fileExists(t, filepath.Join(beadsDir, PortFileName)) {
		t.Errorf("cleanup removed state files while the start lock was held")
	}
	if !fileExists(t, lockFile) {
		t.Errorf("dolt-server.lock was removed")
	}
}

// TestCleanupStalePIDState_ReverifiesUnderLock closes the TOCTOU window: the
// pre-lock inspection sees stale state, but the state changes before the
// removal happens. The re-read under the flock must turn the removal into a
// no-op.
func TestCleanupStalePIDState_ReverifiesUnderLock(t *testing.T) {
	t.Run("pid file vanishes", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		writePIDState(t, beadsDir, strconv.Itoa(deadPID(t)), "13307")

		res, err := cleanupStalePIDState(beadsDir, func() {
			// Simulates another process cleaning up (or a fresh Start()
			// rewriting state) between the check and the removal.
			_ = os.Remove(filepath.Join(beadsDir, PIDFileName))
		})
		if err != nil {
			t.Fatalf("cleanupStalePIDState: %v", err)
		}
		if !res.LockAcquired {
			t.Fatalf("LockAcquired = false, want true")
		}
		if res.Cleaned {
			t.Errorf("Cleaned = true; the re-verification under the lock must veto the removal")
		}
		if !fileExists(t, filepath.Join(beadsDir, PortFileName)) {
			t.Errorf("port file removed even though the re-verification failed")
		}
	})

	t.Run("live server appears", func(t *testing.T) {
		pid := liveDoltPID()
		if pid == 0 {
			t.Skip("no dolt sql-server running on this host")
		}
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		writePIDState(t, beadsDir, strconv.Itoa(deadPID(t)), "13307")

		res, err := cleanupStalePIDState(beadsDir, func() {
			// A new server won the race and wrote its own PID file.
			if err := os.WriteFile(filepath.Join(beadsDir, PIDFileName), []byte(strconv.Itoa(pid)), 0o600); err != nil {
				t.Error(err)
			}
		})
		if err != nil {
			t.Fatalf("cleanupStalePIDState: %v", err)
		}
		if res.Cleaned {
			t.Errorf("Cleaned = true; a server that started under the lock must be preserved")
		}
		if !fileExists(t, filepath.Join(beadsDir, PIDFileName)) || !fileExists(t, filepath.Join(beadsDir, PortFileName)) {
			t.Errorf("cleanup removed the state of the newly started server")
		}
	})
}

// TestCleanupStalePIDState_ConcurrentStartSerialization proves cleanup does not
// break Start()'s mutual exclusion: cleanup and a Start()-shaped critical
// section never overlap, and the lock anchor is never replaced.
func TestCleanupStalePIDState_ConcurrentStartSerialization(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	writePIDState(t, beadsDir, strconv.Itoa(deadPID(t)), "13307")
	lockFile := filepath.Join(beadsDir, "dolt-server.lock")
	lockBefore, err := os.Stat(lockFile)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	inCritical := 0
	overlap := false

	enter := func() {
		mu.Lock()
		inCritical++
		if inCritical > 1 {
			overlap = true
		}
		mu.Unlock()
	}
	leave := func() {
		mu.Lock()
		inCritical--
		mu.Unlock()
	}

	var wg sync.WaitGroup
	// A Start()-shaped competitor: same lock file, same flock discipline.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			if err := lockfile.FlockExclusiveBlocking(f); err != nil {
				t.Error(err)
				return
			}
			enter()
			time.Sleep(5 * time.Millisecond)
			leave()
			_ = lockfile.FlockUnlock(f)
		}()
	}
	// Cleanup runs concurrently; when it gets the lock it is inside the same
	// critical section and must be counted by the same guard.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cleanupStalePIDState(beadsDir, func() {
				enter()
				time.Sleep(5 * time.Millisecond)
				leave()
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if overlap {
		t.Errorf("cleanup and Start()-shaped sections overlapped; serialization is broken")
	}
	lockAfter, err := os.Stat(lockFile)
	if err != nil {
		t.Fatalf("dolt-server.lock was removed: %v", err)
	}
	if !os.SameFile(lockBefore, lockAfter) {
		t.Errorf("dolt-server.lock inode changed during concurrent cleanup")
	}
}

func TestCleanupStalePIDState_ReportsRemovedFiles(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	writePIDState(t, beadsDir, strconv.Itoa(deadPID(t)), "13307")

	res, err := CleanupStalePIDState(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Removed, ",")
	if !strings.Contains(joined, PIDFileName) || !strings.Contains(joined, PortFileName) {
		t.Errorf("Removed = %v, want both %s and %s", res.Removed, PIDFileName, PortFileName)
	}
	if strings.Contains(joined, "dolt-server.lock") {
		t.Errorf("Removed reports the lock file: %v", res.Removed)
	}
}
