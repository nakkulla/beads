package storage

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassifiedErrorPreservesFailureCodeAndCause(t *testing.T) {
	cause := errors.New("remote refused push")
	err := fmt.Errorf("sync: %w", &ClassifiedError{Code: FailureSyncRemoteAhead, Cause: cause})

	code, ok := CodeOf(err)
	if !ok || code != FailureSyncRemoteAhead {
		t.Fatalf("CodeOf() = (%q, %t), want (%q, true)", code, ok, FailureSyncRemoteAhead)
	}
	if !errors.Is(err, cause) {
		t.Fatal("wrapped classified error must preserve its cause")
	}
}

func TestCodeOfReturnsFalseForUnclassifiedError(t *testing.T) {
	if code, ok := CodeOf(errors.New("ordinary failure")); ok || code != "" {
		t.Fatalf("CodeOf() = (%q, %t), want (\"\", false)", code, ok)
	}
}

func TestFailureCodesAreStableLowerSnakeCase(t *testing.T) {
	codes := []FailureCode{
		FailureLockConflict, FailureLocalStoreCorrupt, FailureDatabaseNotFound,
		FailureDatabaseOpenFailed, FailureRemoteNotConfigured, FailureRemoteAuthFailed,
		FailureRemoteUnreachable, FailureRemoteDataMissing, FailureSyncRemoteAhead,
		FailureHistoryDiverged, FailureWorkingSetDirty, FailureDanglingReference,
		FailureSchemaMigrationRequired, FailureOperationFailedUnknown,
	}
	for _, code := range codes {
		if code == "" {
			t.Fatal("failure code must not be empty")
		}
		for _, r := range code {
			if !(r >= 'a' && r <= 'z') && r != '_' {
				t.Errorf("failure code %q is not lower_snake_case", code)
			}
		}
	}
}
