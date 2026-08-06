package doctor

import (
	"fmt"
	"os"
	"strings"

	"github.com/steveyegge/beads/cmd/bd/doctor/fix"
	"github.com/steveyegge/beads/internal/configfile"
)

// ExternalLifecyclePinCheckName is the check name shared by the diagnostic and
// the doctor --fix dispatch switch.
const ExternalLifecyclePinCheckName = "Dolt External Lifecycle Marker"

// CheckExternalLifecyclePin reports a rig whose external sql-server lifecycle is
// still implied by a git-tracked dolt_server_port instead of the explicit
// dolt_server_lifecycle marker, and the two contradictory metadata.json shapes
// that are reported but never auto-resolved.
func CheckExternalLifecyclePin(path string) DoctorCheck {
	beadsDir := ResolveBeadsDirForRepo(path)

	if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
		return DoctorCheck{
			Name:     ExternalLifecyclePinCheckName,
			Status:   StatusOK,
			Message:  "N/A (no .beads directory)",
			Category: CategoryData,
		}
	}

	report := fix.InspectExternalLifecyclePin(beadsDir)

	// Diagnostic-only findings. An empty Fix keeps them off the doctor --fix
	// worklist: both shapes need a human to say which side is the truth.
	var diagnostics []string
	if report.EmbeddedWithMarker {
		diagnostics = append(diagnostics,
			"dolt_mode is embedded while dolt_server_lifecycle is set; embedded wins, so the marker has no effect. "+
				"Remove whichever key does not match how this rig actually runs.")
	}
	if report.UnrecognizedMarker != "" {
		diagnostics = append(diagnostics, fmt.Sprintf(
			"dolt_server_lifecycle=%q is not a recognized value; bd treats any non-empty value as %q "+
				"(auto-start stays off). Fix the value or remove the key.",
			report.UnrecognizedMarker, configfile.DoltServerLifecycleExternal))
	}
	if len(diagnostics) > 0 {
		return DoctorCheck{
			Name:     ExternalLifecyclePinCheckName,
			Status:   StatusWarning,
			Message:  "metadata.json lifecycle configuration is inconsistent",
			Detail:   strings.Join(diagnostics, " "),
			Category: CategoryData,
		}
	}

	if !report.Applicable {
		message := "external lifecycle is recorded explicitly"
		if report.SkipReason != "" {
			message = fmt.Sprintf("N/A (%s)", report.SkipReason)
		}
		return DoctorCheck{
			Name:     ExternalLifecyclePinCheckName,
			Status:   StatusOK,
			Message:  message,
			Category: CategoryData,
		}
	}

	return DoctorCheck{
		Name:    ExternalLifecyclePinCheckName,
		Status:  StatusWarning,
		Message: "External lifecycle is implied by dolt_server_port alone",
		Detail: fmt.Sprintf(
			"metadata.json dolt_server_port=%d is the only signal keeping bd from auto-starting its own dolt sql-server. "+
				"Recording dolt_server_lifecycle=%s makes that explicit and lets the stale port key be dropped later.",
			report.StoredPort, configfile.DoltServerLifecycleExternal),
		Fix:      "Run 'bd doctor --fix' to record dolt_server_lifecycle in metadata.json",
		Category: CategoryData,
	}
}
