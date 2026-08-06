package fix

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/storage/dolt"
)

const usableRemoteYAML = "sync.remote: \"git+https://github.com/org/repo.git\"\n"

// livePIDState is the verdict InspectPIDState produces for a rig whose
// recorded PID is a running dolt sql-server. It is fabricated because a real
// one needs an actual dolt server process, which this package's tests do not
// have; the gate under test consumes the verdict, not the process.
func livePIDState() doltserver.PIDState {
	return doltserver.PIDState{
		PIDFileExists: true,
		RecordedPID:   4242,
		RecordedPort:  3307,
		ProcessAlive:  true,
		IsDoltServer:  true,
		Status:        doltserver.PIDStateLive,
		Reason:        "PID 4242 is a running dolt sql-server",
	}
}

func absentPIDState() doltserver.PIDState {
	return doltserver.PIDState{Status: doltserver.PIDStateAbsent, Reason: "no dolt-server.pid"}
}

// planWithPIDState drives the injectable planner so a test can choose the
// server-liveness verdict. The exported PlanLocalStoreRecovery is exercised
// separately to prove it wires the real inspection in.
func planWithPIDState(beadsDir string, openErr error, pidState doltserver.PIDState) LocalStoreRecoveryPlan {
	return planLocalStoreRecovery(beadsDir, openErr, pidState, dolt.LocalStoreRecoverer(), time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
}

func TestPlanLocalStoreRecovery_RecoverableCorruptLocalRig(t *testing.T) {
	beadsDir := seedLocalStoreRig(t, configfile.DoltModeEmbedded, usableRemoteYAML, true)
	before := snapshotTree(t, beadsDir)

	plan := PlanLocalStoreRecovery(beadsDir, errors.New(corruptManifestOpenErr))

	if !plan.Recoverable {
		t.Fatalf("Recoverable = false, want true (refusal %q)", plan.Refusal)
	}
	if plan.Refusal != "" {
		t.Errorf("Refusal = %q, want empty on a recoverable plan", plan.Refusal)
	}
	if plan.Remote == "" {
		t.Error("Remote is empty, want the configured re-clone source")
	}
	if plan.Database == "" {
		t.Error("Database is empty, want the configured database name")
	}
	if plan.StorePath == "" {
		t.Error("StorePath is empty, want the located store directory")
	}
	if plan.QuarantinePath == "" {
		t.Fatal("QuarantinePath is empty, want a timestamped destination")
	}

	// Planning is read-only: nothing may move before the confirmation gate.
	assertTreeUnchanged(t, beadsDir, before)
}

// The quarantine destination is the acceptance-critical path assertion: a
// backup left inside .beads/ is re-scanned by the engine and re-synced by bd.
func TestPlanLocalStoreRecovery_QuarantineIsOutsideBeadsDir(t *testing.T) {
	beadsDir := seedLocalStoreRig(t, configfile.DoltModeEmbedded, usableRemoteYAML, true)

	plan := PlanLocalStoreRecovery(beadsDir, errors.New(corruptManifestOpenErr))
	if !plan.Recoverable {
		t.Fatalf("Recoverable = false, want true (refusal %q)", plan.Refusal)
	}

	absBeads, err := filepath.Abs(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	absQuarantine, err := filepath.Abs(plan.QuarantinePath)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(absBeads, absQuarantine)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q): %v", absBeads, absQuarantine, err)
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("quarantine path %q is inside the beads dir %q (rel %q)", absQuarantine, absBeads, rel)
	}

	// And it must carry a timestamp so repeated runs never collide.
	if !strings.ContainsAny(filepath.Base(absQuarantine), "0123456789") {
		t.Errorf("quarantine dir name %q carries no timestamp", filepath.Base(absQuarantine))
	}
}

func TestPlanLocalStoreRecovery_Refusals(t *testing.T) {
	tests := map[string]struct {
		doltMode      string
		yaml          string
		corrupt       bool
		openErr       string
		livePID       bool
		wantSubstring string
	}{
		"no sync remote leaves the rig diagnostic-only": {
			doltMode:      configfile.DoltModeEmbedded,
			yaml:          "# nothing configured\n",
			corrupt:       true,
			openErr:       corruptManifestOpenErr,
			wantSubstring: "no sync remote",
		},
		"server-mode rig is refused": {
			doltMode:      configfile.DoltModeServer,
			yaml:          usableRemoteYAML,
			corrupt:       true,
			openErr:       corruptManifestOpenErr,
			wantSubstring: "server-mode",
		},
		"transient failure is not a corruption repair": {
			doltMode:      configfile.DoltModeEmbedded,
			yaml:          usableRemoteYAML,
			corrupt:       false,
			openErr:       unreachableOpenErr,
			wantSubstring: "corruption",
		},
		"healthy store has nothing to repair": {
			doltMode:      configfile.DoltModeEmbedded,
			yaml:          usableRemoteYAML,
			corrupt:       false,
			openErr:       "",
			wantSubstring: "corruption",
		},
		"live dolt server is refused": {
			doltMode:      configfile.DoltModeEmbedded,
			yaml:          usableRemoteYAML,
			corrupt:       true,
			openErr:       corruptManifestOpenErr,
			livePID:       true,
			wantSubstring: "server",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			beadsDir := seedLocalStoreRig(t, tt.doltMode, tt.yaml, tt.corrupt)
			pidState := absentPIDState()
			if tt.livePID {
				pidState = livePIDState()
			}
			before := snapshotTree(t, beadsDir)

			var openErr error
			if tt.openErr != "" {
				openErr = errors.New(tt.openErr)
			}
			plan := planWithPIDState(beadsDir, openErr, pidState)

			if plan.Recoverable {
				t.Fatalf("Recoverable = true, want a refusal for %q", name)
			}
			if plan.Refusal == "" {
				t.Fatal("Refusal is empty, want an explanation")
			}
			if !strings.Contains(strings.ToLower(plan.Refusal), tt.wantSubstring) {
				t.Errorf("Refusal = %q, want it to mention %q", plan.Refusal, tt.wantSubstring)
			}
			assertTreeUnchanged(t, beadsDir, before)
		})
	}
}

// The exported entry point must read the rig's real PID state, not a default.
// A dead recorded PID is not a live server, so the gate must let it through;
// this also proves PlanLocalStoreRecovery consults doltserver at all.
func TestPlanLocalStoreRecovery_ExportedEntryPointReadsRealPIDState(t *testing.T) {
	beadsDir := seedLocalStoreRig(t, configfile.DoltModeEmbedded, usableRemoteYAML, true)

	// PID 0 is never a live process, so InspectPIDState reports a stale state.
	pidPath := filepath.Join(beadsDir, doltserver.PIDFileName)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(0)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := doltserver.InspectPIDState(beadsDir).Status; got == doltserver.PIDStateLive {
		t.Fatalf("fixture PID state = %q, want a non-live state", got)
	}

	plan := PlanLocalStoreRecovery(beadsDir, errors.New(corruptManifestOpenErr))
	if !plan.Recoverable {
		t.Fatalf("Recoverable = false for a stale PID file, want true (refusal %q)", plan.Refusal)
	}
}

// A corrupt rig whose store directory is already gone has nothing to move; the
// re-clone alone is the repair, so the planner must not invent a quarantine.
func TestPlanLocalStoreRecovery_MissingStoreDirNeedsNoQuarantine(t *testing.T) {
	beadsDir := seedLocalStoreRig(t, configfile.DoltModeEmbedded, usableRemoteYAML, true)
	if err := os.RemoveAll(filepath.Join(beadsDir, "dolt", "beads")); err != nil {
		t.Fatal(err)
	}

	plan := PlanLocalStoreRecovery(beadsDir, errors.New(corruptManifestOpenErr))

	if !plan.Recoverable {
		t.Fatalf("Recoverable = false, want true (refusal %q)", plan.Refusal)
	}
	if plan.StorePath != "" {
		t.Errorf("StorePath = %q, want empty when no store directory exists", plan.StorePath)
	}
	if plan.QuarantinePath != "" {
		t.Errorf("QuarantinePath = %q, want empty when there is nothing to move", plan.QuarantinePath)
	}
}

// QuarantineLocalStore is the only entry point that moves data. It must refuse
// a plan it did not approve, so a caller cannot skip the gate by hand-building
// one.
func TestQuarantineLocalStore_RefusesUnapprovedPlan(t *testing.T) {
	beadsDir := seedLocalStoreRig(t, configfile.DoltModeEmbedded, "# nothing configured\n", true)
	before := snapshotTree(t, beadsDir)

	plan := PlanLocalStoreRecovery(beadsDir, errors.New(corruptManifestOpenErr))
	if plan.Recoverable {
		t.Fatal("fixture should not be recoverable (no remote)")
	}

	if err := QuarantineLocalStore(plan); err == nil {
		t.Fatal("expected QuarantineLocalStore to refuse a non-recoverable plan")
	}
	assertTreeUnchanged(t, beadsDir, before)
}

func TestQuarantineLocalStore_MovesStoreOutsideBeadsDir(t *testing.T) {
	beadsDir := seedLocalStoreRig(t, configfile.DoltModeEmbedded, usableRemoteYAML, true)

	plan := PlanLocalStoreRecovery(beadsDir, errors.New(corruptManifestOpenErr))
	if !plan.Recoverable {
		t.Fatalf("Recoverable = false, want true (refusal %q)", plan.Refusal)
	}

	if err := QuarantineLocalStore(plan); err != nil {
		t.Fatalf("QuarantineLocalStore: %v", err)
	}

	if _, err := os.Stat(plan.StorePath); !os.IsNotExist(err) {
		t.Errorf("store %q still present after quarantine (stat err %v)", plan.StorePath, err)
	}
	if _, err := os.Stat(plan.QuarantinePath); err != nil {
		t.Errorf("quarantined store missing at %q: %v", plan.QuarantinePath, err)
	}
	// Nothing corrupt may be left anywhere under .beads/.
	if _, err := os.Stat(filepath.Join(beadsDir, "dolt", "beads")); !os.IsNotExist(err) {
		t.Errorf("a copy of the corrupt store remains under .beads/ (stat err %v)", err)
	}
}
