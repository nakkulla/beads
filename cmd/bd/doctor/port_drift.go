package doctor

import (
	"fmt"
	"os"

	"github.com/steveyegge/beads/cmd/bd/doctor/fix"
)

// DoltPortDriftCheckName is the check name shared by the diagnostic and the
// doctor --fix dispatch switch.
const DoltPortDriftCheckName = "Dolt Port Drift"

// CheckDoltPortDrift reports a git-tracked metadata.json dolt_server_port that
// disagrees with the port bd actually connects on.
//
// The gate is configfile.Config.IsDoltServerMode() (dolt_mode based), not
// doltserver.ResolveServerMode: ResolveServerMode calls any rig with an
// explicit dolt_server_port External, which is the very key under
// investigation — using it here would be circular and over-broad.
func CheckDoltPortDrift(path string) DoctorCheck {
	beadsDir := ResolveBeadsDirForRepo(path)

	if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
		return DoctorCheck{
			Name:     DoltPortDriftCheckName,
			Status:   StatusOK,
			Message:  "N/A (no .beads directory)",
			Category: CategoryData,
		}
	}

	report := fix.InspectDoltPortDrift(beadsDir)

	if !report.Applicable || !report.Drift {
		message := "metadata.json port matches the effective port"
		if report.SkipReason != "" {
			message = fmt.Sprintf("N/A (%s)", report.SkipReason)
		}
		return DoctorCheck{
			Name:     DoltPortDriftCheckName,
			Status:   StatusOK,
			Message:  message,
			Category: CategoryData,
		}
	}

	detail := fmt.Sprintf("metadata.json dolt_server_port=%d; effective port %d comes from %s",
		report.StoredPort, report.EffectivePort, report.AuthoritySource)

	if !report.Removable {
		// Diagnostic only: an empty Fix keeps this off the doctor --fix
		// worklist so a refusal never turns into a failed fix attempt.
		return DoctorCheck{
			Name:     DoltPortDriftCheckName,
			Status:   StatusWarning,
			Message:  fmt.Sprintf("Stale dolt_server_port %d (effective port is %d)", report.StoredPort, report.EffectivePort),
			Detail:   fmt.Sprintf("%s. Not auto-fixable: %s", detail, report.BlockedReason),
			Category: CategoryData,
		}
	}

	return DoctorCheck{
		Name:     DoltPortDriftCheckName,
		Status:   StatusWarning,
		Message:  fmt.Sprintf("Stale dolt_server_port %d (effective port is %d)", report.StoredPort, report.EffectivePort),
		Detail:   detail,
		Fix:      "Run 'bd doctor --fix' to drop the stale dolt_server_port key from metadata.json",
		Category: CategoryData,
	}
}
