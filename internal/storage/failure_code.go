package storage

import "errors"

// FailureCode is a stable, engine-agnostic fact about an in-scope storage
// failure. It deliberately does not prescribe recovery policy.
type FailureCode string

const (
	FailureLockConflict            FailureCode = "lock_conflict"
	FailureLocalStoreCorrupt       FailureCode = "local_store_corrupt"
	FailureDatabaseNotFound        FailureCode = "database_not_found"
	FailureDatabaseOpenFailed      FailureCode = "database_open_failed"
	FailureRemoteNotConfigured     FailureCode = "remote_not_configured"
	FailureRemoteAuthFailed        FailureCode = "remote_auth_failed"
	FailureRemoteUnreachable       FailureCode = "remote_unreachable"
	FailureRemoteDataMissing       FailureCode = "remote_data_missing"
	FailureSyncRemoteAhead         FailureCode = "sync_remote_ahead"
	FailureHistoryDiverged         FailureCode = "history_diverged"
	FailureWorkingSetDirty         FailureCode = "working_set_dirty"
	FailureDanglingReference       FailureCode = "dangling_reference"
	FailureSchemaMigrationRequired FailureCode = "schema_migration_required"
	FailureOperationFailedUnknown  FailureCode = "operation_failed_unknown"
)

// ClassifiedError attaches a stable failure code without losing the original
// error chain. CLI and doctor layers project their own operation-specific
// evidence rather than coupling it to this storage type.
type ClassifiedError struct {
	Code  FailureCode
	Cause error
}

func (e *ClassifiedError) Error() string {
	if e == nil || e.Cause == nil {
		return string(e.Code)
	}
	return e.Cause.Error()
}

func (e *ClassifiedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// CodeOf returns the first classified error in err's wrapping chain.
func CodeOf(err error) (FailureCode, bool) {
	var classified *ClassifiedError
	if errors.As(err, &classified) && classified != nil && classified.Code != "" {
		return classified.Code, true
	}
	return "", false
}
