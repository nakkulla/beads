package doctor

import (
	"fmt"
	"os"

	"github.com/steveyegge/beads/internal/doltserver"
)

// StaleServerPIDStateCheckName is the check name shared by the diagnostic and
// the doctor --fix dispatch switch.
const StaleServerPIDStateCheckName = "Stale Server PID State"

// staleServerPIDStateFixHint is the --fix hint. Its presence is what puts the
// check on the doctor --fix worklist, so it must stay empty for healthy rigs.
const staleServerPIDStateFixHint = "Run 'bd doctor --fix' to remove the stale dolt-server.pid/.port files " +
	"(dolt-server.lock is the start-serialization anchor and is never removed)"

// CheckStaleServerPIDState reports dolt server lifecycle files left behind by
// a process that is gone: a dead PID, a PID reused by an unrelated process, or
// an unparseable dolt-server.pid.
//
// The diagnostic is read-only — it never deletes state — so a live server is
// reported as healthy and the repair stays behind doctor --fix.
func CheckStaleServerPIDState(path string) DoctorCheck {
	beadsDir := ResolveBeadsDirForRepo(path)

	if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
		return DoctorCheck{
			Name:     StaleServerPIDStateCheckName,
			Status:   StatusOK,
			Message:  "N/A (no .beads directory)",
			Category: CategoryRuntime,
		}
	}

	state := doltserver.InspectPIDState(beadsDir)

	if !state.Stale {
		message := "No stale dolt server state"
		if state.Status == doltserver.PIDStateLive {
			message = fmt.Sprintf("Dolt server running (PID %d)", state.RecordedPID)
			if state.RecordedPort > 0 {
				message = fmt.Sprintf("Dolt server running (PID %d, port %d)", state.RecordedPID, state.RecordedPort)
			}
		}
		return DoctorCheck{
			Name:     StaleServerPIDStateCheckName,
			Status:   StatusOK,
			Message:  message,
			Detail:   state.Reason,
			Category: CategoryRuntime,
		}
	}

	message := "Stale dolt server state"
	switch state.Status {
	case doltserver.PIDStateDead:
		message = fmt.Sprintf("Stale %s: PID %d is not running", doltserver.PIDFileName, state.RecordedPID)
	case doltserver.PIDStateReused:
		message = fmt.Sprintf("Stale %s: PID %d belongs to another process", doltserver.PIDFileName, state.RecordedPID)
	case doltserver.PIDStateCorrupt:
		message = fmt.Sprintf("Corrupt %s", doltserver.PIDFileName)
	}

	detail := state.Reason
	if state.PortFileExists {
		detail = fmt.Sprintf("%s; %s records port %s", state.Reason, doltserver.PortFileName, state.PortFileRaw)
	}

	return DoctorCheck{
		Name:     StaleServerPIDStateCheckName,
		Status:   StatusWarning,
		Message:  message,
		Detail:   detail,
		Fix:      staleServerPIDStateFixHint,
		Category: CategoryRuntime,
	}
}
