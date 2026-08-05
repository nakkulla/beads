package doctor

import (
	"fmt"
	"os"
	"strings"

	"github.com/steveyegge/beads/cmd/bd/doctor/fix"
)

// SyncRemoteShapeCheckName is the check name shared by the diagnostic and the
// doctor --fix dispatch switch.
const SyncRemoteShapeCheckName = "Sync Remote Shape"

// syncRemoteShapeFixHint is the --fix hint. Its presence is what puts the check
// on the doctor --fix worklist, so it must stay empty for the diagnostic-only
// findings.
const syncRemoteShapeFixHint = "Run 'bd doctor --fix' to unset " + fix.SyncRemoteKey +
	" (the value is commented out in config.yaml, not deleted)"

// CheckSyncRemoteShape reports a sync remote that is not a Dolt remote, and a
// server-mode rig that carries a routine sync remote at all.
//
// Only the second finding is auto-fixable. An unparseable remote is reported
// for review and never rewritten — see fix.InspectSyncRemoteShape for why.
func CheckSyncRemoteShape(path string) DoctorCheck {
	beadsDir := ResolveBeadsDirForRepo(path)

	if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
		return DoctorCheck{
			Name:     SyncRemoteShapeCheckName,
			Status:   StatusOK,
			Message:  "N/A (no .beads directory)",
			Category: CategoryData,
		}
	}

	report := fix.InspectSyncRemoteShape(beadsDir)

	if report.Remote == "" {
		return DoctorCheck{
			Name:     SyncRemoteShapeCheckName,
			Status:   StatusOK,
			Message:  "No sync remote configured",
			Category: CategoryData,
		}
	}

	if !report.NotDoltRemote && !report.ServerModeRemote {
		return DoctorCheck{
			Name:     SyncRemoteShapeCheckName,
			Status:   StatusOK,
			Message:  fmt.Sprintf("%s is a Dolt-compatible remote", report.SourceKey),
			Category: CategoryData,
		}
	}

	var messages, details []string

	if report.NotDoltRemote {
		messages = append(messages, fmt.Sprintf("%s=%s is not a Dolt remote", report.SourceKey, report.Remote))
		details = append(details, fmt.Sprintf(
			"Dolt reads the configured value verbatim; as a Dolt remote it would have to be %q. "+
				"Reported for review only — bd never rewrites the value, because a plain http(s) URL can also be a remotesapi endpoint.",
			report.Normalized))
	}

	if report.ServerModeRemote {
		messages = append(messages, fmt.Sprintf("server-mode rig carries a routine %s", report.SourceKey))
		if report.Unsettable {
			details = append(details, fmt.Sprintf(
				"This rig is in Dolt server mode, so it syncs through its server; %s=%s is the auto-fixable finding.",
				report.SourceKey, report.Remote))
		} else {
			details = append(details, fmt.Sprintf(
				"This rig is in Dolt server mode, so it syncs through its server. %s is the deprecated fallback key and is not auto-fixable; remove it manually.",
				report.SourceKey))
		}
	}

	check := DoctorCheck{
		Name:     SyncRemoteShapeCheckName,
		Status:   StatusWarning,
		Message:  strings.Join(messages, "; "),
		Detail:   strings.Join(details, " "),
		Category: CategoryData,
	}
	if report.Unsettable {
		check.Fix = syncRemoteShapeFixHint
	}
	return check
}
