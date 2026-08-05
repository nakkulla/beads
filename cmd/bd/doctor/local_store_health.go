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
// Diagnostic only. Fix stays empty so the check never reaches the
// doctor --fix worklist: quarantining and re-cloning a store moves user data
// and is not implemented here.
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

	return localStoreHealthCheck(fix.InspectLocalStoreHealth(beadsDir, ss.OpenErr()))
}

func localStoreHealthCheck(report fix.LocalStoreHealthReport) DoctorCheck {
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
		check.Message = "Dolt store failed to open with a corruption signature; " + remoteClause(report)
		details = append(details, fmt.Sprintf("Corruption signature: %q.", report.Signature))
		if len(report.CorruptDirs) > 0 {
			details = append(details, "Corrupt Dolt directories:\n  "+strings.Join(report.CorruptDirs, "\n  "))
		}
	} else {
		check.Status = StatusWarning
		check.Message = "Dolt store did not open (transient failure); " + remoteClause(report)
		details = append(details, "No corruption signature in the open error — this reads as a server, connectivity, "+
			"permission, or configuration problem. Retry after 'bd dolt start'.")
	}

	if report.RemoteUsable {
		details = append(details, fmt.Sprintf(
			"%s=%s is configured and can serve as a re-clone source, but no automated repair is wired up here — this check is diagnostic only.",
			report.RemoteKey, report.Remote))
	} else {
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

// remoteClause is the part of the message that differs by re-clone
// availability, so an operator can tell a repairable rig from an unrepairable
// one without reading the detail.
func remoteClause(report fix.LocalStoreHealthReport) string {
	if report.RemoteUsable {
		return fmt.Sprintf("re-clone source %s is configured", report.RemoteKey)
	}
	return "no sync remote is configured, so it cannot be re-cloned"
}
