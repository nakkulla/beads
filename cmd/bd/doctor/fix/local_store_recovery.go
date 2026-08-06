package fix

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/dolt"
)

// QuarantineDirName is the directory, created as a sibling of .beads/, that
// holds quarantined stores.
//
// It is deliberately outside .beads/: a damaged store parked inside .beads/ is
// still inside the tree the engine scans for databases and the tree bd syncs,
// so the "backup" would keep the corruption live. Being a sibling also keeps it
// visible in the workspace, so an operator finds the preserved data without
// being told where it went.
const QuarantineDirName = ".beads-quarantine"

// quarantineTimestampFormat matches the format the existing integrity recovery
// path uses, so quarantined artifacts sort and read the same way.
const quarantineTimestampFormat = "20060102T150405Z"

// LocalStoreRecoveryPlan is the decision produced for a rig whose local store
// failed to open: whether bd may repair it, and exactly what the repair would
// move and re-clone.
//
// Planning is read-only. Nothing on disk changes until QuarantineLocalStore is
// called with a recoverable plan, which is what lets `bd doctor` (and
// `--dry-run`) describe a destructive repair without performing it.
type LocalStoreRecoveryPlan struct {
	// BeadsDir is the rig the plan was made for.
	BeadsDir string
	// Report is the Phase-5 diagnosis the plan was derived from.
	Report LocalStoreHealthReport

	// Recoverable reports whether every gate passed. When false, Refusal says
	// which gate stopped it and the plan must never be executed.
	Recoverable bool
	// Refusal is the operator-facing reason a repair is not offered.
	Refusal string

	// Remote is the re-clone source, RemoteKey the config key it came from.
	Remote    string
	RemoteKey string
	// Database is the resolved Dolt database name and DataDir the directory it
	// lives in, resolved the same way the clone path resolves them so the
	// quarantine and the re-clone target the same location.
	Database string
	DataDir  string

	// StorePath is the located store directory to move. Empty when the store
	// directory is already gone, in which case the re-clone alone is the repair.
	StorePath string
	// QuarantinePath is where StorePath will be moved. Empty exactly when
	// StorePath is.
	QuarantinePath string
}

// PlanLocalStoreRecovery evaluates whether the rig at beadsDir may have its
// local store quarantined and re-cloned, given the store-open error observed
// for it.
//
// Callers pass a freshly observed openErr rather than a stored verdict: the
// repair runs after the diagnosis, and a store that started working in between
// must not be destroyed.
func PlanLocalStoreRecovery(beadsDir string, openErr error) LocalStoreRecoveryPlan {
	return planLocalStoreRecovery(beadsDir, openErr, doltserver.InspectPIDState(beadsDir), dolt.LocalStoreRecoverer(), time.Now().UTC())
}

// planLocalStoreRecovery is the injectable form. The PID state and the clock
// are parameters so the live-server gate and the timestamped destination are
// testable without a real dolt server.
func planLocalStoreRecovery(
	beadsDir string,
	openErr error,
	pidState doltserver.PIDState,
	recoverer storage.LocalStoreRecoverer,
	now time.Time,
) LocalStoreRecoveryPlan {
	report := InspectLocalStoreHealth(beadsDir, openErr)
	plan := LocalStoreRecoveryPlan{
		BeadsDir:  beadsDir,
		Report:    report,
		Remote:    report.Remote,
		RemoteKey: report.RemoteKey,
	}

	// Gate 1 — corruption. The repair replaces the store wholesale, so it may
	// only run on a positively identified on-disk corruption. A transient
	// failure (server down, permissions, connectivity) would be "repaired" by
	// destroying a healthy store.
	if report.Class != StoreOpenClassCorrupt {
		plan.Refusal = localStoreNotCorruptRefusal(report)
		return plan
	}

	// Gate 2 — server mode. Follows serverModeIntegrityRecoveryGuard: on a rig
	// backed by an external Dolt server the local tree is not the authority,
	// and repairing it can replace the wrong root. Server-mode recovery needs a
	// stop → quarantine → re-clone → restart orchestration that is out of scope
	// here.
	if report.ServerMode {
		plan.Refusal = fmt.Sprintf(
			"automatic store recovery is disabled for server-mode rigs because it can replace the wrong Dolt root; "+
				"this rig syncs through its Dolt server, so verify the configured database %q on that server instead of rebuilding %s",
			resolveRecoveryDatabase(beadsDir), doltserver.ResolveDoltDir(beadsDir))
		return plan
	}

	// Gate 3 — live server. Even on a local rig, a running dolt sql-server
	// holds the store open; moving it out from under a live process is the
	// stop/restart orchestration this version does not do.
	if pidState.Status == doltserver.PIDStateLive {
		plan.Refusal = fmt.Sprintf(
			"a dolt sql-server (PID %d) is running for this rig; stop it with 'bd dolt stop' before recovering the store, "+
				"because moving the store out from under a live server is not something bd will orchestrate automatically",
			pidState.RecordedPID)
		return plan
	}

	// Gate 4 — a re-clone source. Without a remote the quarantine would strand
	// the rig with no data at all, so the check stays diagnostic.
	if !report.RemoteUsable {
		plan.Refusal = "no sync remote is configured, so there is nothing to re-clone from; " +
			"the damaged store is left in place rather than moved aside into an unrecoverable state"
		return plan
	}

	plan.Database = resolveRecoveryDatabase(beadsDir)
	plan.DataDir = doltserver.ResolveDoltDir(beadsDir)

	loc, err := recoverer.LocateLocalStore(plan.DataDir, plan.Database)
	if err != nil {
		plan.Refusal = fmt.Sprintf("could not determine which directory holds the damaged store: %v", err)
		return plan
	}
	if loc.Found {
		plan.StorePath = loc.Path
		plan.QuarantinePath = filepath.Join(
			filepath.Dir(beadsDir),
			QuarantineDirName,
			fmt.Sprintf("%s-%s", plan.Database, now.Format(quarantineTimestampFormat)),
		)
	}
	// A missing store directory is still recoverable: there is simply nothing
	// to move, and the re-clone alone restores the rig.

	plan.Recoverable = true
	return plan
}

func localStoreNotCorruptRefusal(report LocalStoreHealthReport) string {
	if report.Class == StoreOpenClassNone {
		return "the local store opened successfully, so there is no corruption to recover from"
	}
	return "the store-open failure carries no corruption signature, so this is not a corruption repair; " +
		"it reads as a server, connectivity, permission, or configuration problem — retry after 'bd dolt start'"
}

func resolveRecoveryDatabase(beadsDir string) string {
	cfg, err := configfile.Load(beadsDir)
	if err != nil || cfg == nil {
		return configfile.DefaultDoltDatabase
	}
	if name := strings.TrimSpace(cfg.GetDoltDatabase()); name != "" {
		return name
	}
	return configfile.DefaultDoltDatabase
}

// QuarantineLocalStore moves the damaged store described by a recoverable plan
// out of the way, through the storage-boundary capability.
//
// This is the only function in the recovery path that changes the filesystem,
// and it refuses any plan the gates did not approve, so a caller cannot reach
// the destructive step by hand-assembling a plan.
//
// It is a no-op for a plan with no store directory to move.
func QuarantineLocalStore(plan LocalStoreRecoveryPlan) error {
	return quarantineLocalStore(plan, dolt.LocalStoreRecoverer())
}

func quarantineLocalStore(plan LocalStoreRecoveryPlan, recoverer storage.LocalStoreRecoverer) error {
	if !plan.Recoverable {
		return fmt.Errorf("refusing to move the store: recovery was not approved (%s)", plan.Refusal)
	}
	if plan.StorePath == "" {
		return nil
	}
	if plan.QuarantinePath == "" {
		return fmt.Errorf("refusing to move %s: no quarantine destination was planned", plan.StorePath)
	}
	if err := assertOutsideBeadsDir(plan.BeadsDir, plan.QuarantinePath); err != nil {
		return err
	}

	loc, err := recoverer.LocateLocalStore(plan.DataDir, plan.Database)
	if err != nil {
		return fmt.Errorf("re-locating the damaged store before moving it: %w", err)
	}
	if !loc.Found {
		return fmt.Errorf("the store at %s is no longer there (%s); nothing was moved", plan.StorePath, loc.Reason)
	}
	if loc.Path != plan.StorePath {
		return fmt.Errorf("the store moved between planning and repair (planned %s, found %s); nothing was moved", plan.StorePath, loc.Path)
	}

	return recoverer.QuarantineLocalStore(loc, plan.QuarantinePath)
}

// assertOutsideBeadsDir enforces the destination invariant at the point of use
// rather than trusting the planner, so a plan that was constructed or mutated
// elsewhere still cannot park a damaged store inside .beads/.
func assertOutsideBeadsDir(beadsDir, destination string) error {
	absBeads, err := filepath.Abs(beadsDir)
	if err != nil {
		return fmt.Errorf("resolve beads dir %s: %w", beadsDir, err)
	}
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve quarantine destination %s: %w", destination, err)
	}
	rel, err := filepath.Rel(absBeads, absDest)
	if err != nil {
		return fmt.Errorf("compare %s against %s: %w", absDest, absBeads, err)
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return fmt.Errorf("refusing to quarantine into %s: the destination must be outside %s", absDest, absBeads)
	}
	return nil
}

// DescribeLocalStoreRecovery renders the operator-facing summary of what a
// repair would do. Used for the check's Fix text, so `bd doctor` and
// `bd doctor --fix --dry-run` state the destructive action before it happens.
func DescribeLocalStoreRecovery(plan LocalStoreRecoveryPlan) string {
	if !plan.Recoverable {
		return ""
	}
	if plan.StorePath == "" {
		return fmt.Sprintf("Re-clone the missing Dolt store %q from %s (%s).", plan.Database, plan.Remote, plan.RemoteKey)
	}
	return fmt.Sprintf(
		"Move the damaged Dolt store %s to %s (outside .beads/), then re-clone %q from %s (%s). The quarantined copy is kept, not deleted.",
		plan.StorePath, plan.QuarantinePath, plan.Database, plan.Remote, plan.RemoteKey)
}
