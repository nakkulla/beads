package dolt

import (
	"errors"
	"strings"

	"github.com/steveyegge/beads/internal/storage"
)

// ClassifyFailureCode projects a typed storage failure when one is available.
// The text fallback is intentionally narrow because it bridges legacy Dolt
// diagnostics only; callers must treat an unrecognized failure as unknown.
func ClassifyFailureCode(err error) storage.FailureCode {
	if code, ok := storage.CodeOf(err); ok {
		return code
	}
	if err == nil {
		return storage.FailureOperationFailedUnknown
	}

	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, ErrDanglingReference),
		strings.Contains(message, "dangling chunk"),
		strings.Contains(message, "dangling reference"),
		strings.Contains(message, "referenced but not present"):
		return storage.FailureDanglingReference
	case strings.Contains(message, "exclusive lock"),
		strings.Contains(message, "database is locked"):
		return storage.FailureLockConflict
	case strings.Contains(message, "non-fast-forward"),
		strings.Contains(message, "non fast forward"):
		return storage.FailureSyncRemoteAhead
	case strings.Contains(message, "no common ancestor"),
		strings.Contains(message, "can't find common ancestor"),
		strings.Contains(message, "cannot find common ancestor"):
		return storage.FailureHistoryDiverged
	case strings.Contains(message, "blob not found"),
		strings.Contains(message, "missing chunk") && strings.Contains(message, "manifest"):
		return storage.FailureRemoteDataMissing
	case strings.Contains(message, "i/o timeout"),
		strings.Contains(message, "connection refused"),
		strings.Contains(message, "no such host"),
		strings.Contains(message, "context deadline exceeded"):
		return storage.FailureRemoteUnreachable
	case strings.Contains(message, "authentication failed"),
		strings.Contains(message, "access denied"),
		strings.Contains(message, "unauthorized"):
		return storage.FailureRemoteAuthFailed
	case strings.Contains(message, "working set is dirty"),
		strings.Contains(message, "uncommitted changes"):
		return storage.FailureWorkingSetDirty
	case strings.Contains(message, "database not found") && strings.Contains(message, "dolt"):
		return storage.FailureDatabaseNotFound
	case strings.Contains(message, "corrupt manifest with no recoverable data"),
		strings.Contains(message, "dolt journal corruption detected"),
		strings.Contains(message, "root hash doesn't exist"),
		strings.Contains(message, "corrupted journal"):
		return storage.FailureLocalStoreCorrupt
	default:
		return storage.FailureOperationFailedUnknown
	}
}
