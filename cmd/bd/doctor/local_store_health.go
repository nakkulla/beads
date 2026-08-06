package doctor

import (
	"fmt"
	"os"
	"strings"

	"github.com/steveyegge/beads/cmd/bd/doctor/fix"
)

// LocalStoreHealthCheckName is the check name for the local store-open
// diagnostic.
const LocalStoreHealthCheckName = "Local Store Health"

// manualStoreRecoveryHint is the guidance printed when no re-clone source is
// configured, i.e. when bd could not repair the store even if the repair
// existed.
const manualStoreRecoveryHint = "bd cannot rebuild this store: no sync remote is configured, so there is nothing to re-clone from. " +
	"Manual recovery: move the damaged store aside (to a path outside .beads/), then either restore a JSONL backup " +
	"from .beads/backup/ or set sync.remote in .beads/config.yaml and run 'bd bootstrap --dry-run' followed by 'bd bootstrap'."

// CheckLocalStoreHealth reports the store-open failure that SharedStore
// otherwise only expresses as a nil store, classified as on-disk corruption or
// a transient failure, together with whether a re-clone source is configured.
//
// The check itself is read-only. It carries a Fix only when every recovery gate
// passes (positively identified corruption, a local non-server rig with no live
// dolt server, and a configured remote); on any other rig Fix stays empty, so
// the check never reaches the doctor --fix worklist and the damaged store is
// reported rather than moved.
func CheckLocalStoreHealth(path string, ss *SharedStore) DoctorCheck {
	beadsDir := beadsDirFromSharedStore(path, ss)

	if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
		return DoctorCheck{
			Name:     LocalStoreHealthCheckName,
			Status:   StatusOK,
			Message:  "N/A (no .beads directory)",
			Category: CategoryCore,
		}
	}

	return localStoreHealthCheck(fix.PlanLocalStoreRecovery(beadsDir, ss.OpenErr()))
}

func localStoreHealthCheck(plan fix.LocalStoreRecoveryPlan) DoctorCheck {
	report := plan.Report

	if report.Class == fix.StoreOpenClassNone {
		return DoctorCheck{
			Name:     LocalStoreHealthCheckName,
			Status:   StatusOK,
			Message:  "No store open failure recorded",
			Category: CategoryCore,
		}
	}

	check := DoctorCheck{
		Name:     LocalStoreHealthCheckName,
		Category: CategoryCore,
	}

	var details []string

	if report.Class == fix.StoreOpenClassCorrupt {
		check.Status = StatusError
		check.Message = "Dolt store failed to open with a corruption signature; " + remoteClause(plan)
		details = append(details, fmt.Sprintf("Corruption signature: %q.", report.Signature))
		if len(report.CorruptDirs) > 0 {
			details = append(details, "Corrupt Dolt directories:\n  "+strings.Join(report.CorruptDirs, "\n  "))
		}
	} else {
		check.Status = StatusWarning
		check.Message = "Dolt store did not open (transient failure); " + remoteClause(plan)
		details = append(details, "No corruption signature in the open error — this reads as a server, connectivity, "+
			"permission, or configuration problem. Retry after 'bd dolt start'.")
	}

	if plan.Recoverable {
		// Only a recoverable plan gets a Fix, which is what admits this check
		// to the doctor --fix worklist. The move itself still happens only
		// behind doctor --fix's confirmation prompt or --yes.
		check.Fix = fix.DescribeLocalStoreRecovery(plan)
		details = append(details, fmt.Sprintf(
			"%s=%s is the configured re-clone source. 'bd doctor --fix' moves the damaged store to %s (outside .beads/) and re-clones it; the quarantined copy is kept, not deleted.",
			report.RemoteKey, report.Remote, plan.QuarantinePath))
	} else if plan.Refusal != "" {
		details = append(details, "No automatic repair: "+plan.Refusal+".")
	}

	if !report.RemoteUsable {
		details = append(details, manualStoreRecoveryHint)
	}

	if report.ServerMode {
		details = append(details, "This rig is in Dolt server mode: the local .beads/dolt tree is not the recovery target; "+
			"check the server this rig connects to.")
	}

	if report.OpenErr != nil {
		details = append(details, "Open error: "+report.OpenErr.Error())
	}

	check.Detail = strings.Join(details, " ")
	return check
}

// remoteClause is the part of the message that differs by repairability, so an
// operator can tell a rig doctor --fix will rebuild from one it will only
// report on, without reading the detail.
func remoteClause(plan fix.LocalStoreRecoveryPlan) string {
	if plan.Recoverable {
		return "'bd doctor --fix' can quarantine and re-clone it"
	}
	if !plan.Report.RemoteUsable {
		return "no sync remote is configured, so it cannot be re-cloned"
	}
	return "automatic repair is not available"
}
