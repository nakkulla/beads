package dolt

import (
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

func TestClassifyFailureCodeUsesTypedCodeBeforeLegacyText(t *testing.T) {
	err := &storage.ClassifiedError{Code: storage.FailureRemoteAuthFailed, Cause: errors.New("non-fast-forward")}
	if got := ClassifyFailureCode(err); got != storage.FailureRemoteAuthFailed {
		t.Fatalf("ClassifyFailureCode() = %q, want typed %q", got, storage.FailureRemoteAuthFailed)
	}
}

func TestClassifyFailureCodeLegacySignatures(t *testing.T) {
	tests := []struct {
		name string
		err  string
		want storage.FailureCode
	}{
		{"lock", "Error acquiring exclusive lock on database", storage.FailureLockConflict},
		{"remote ahead", "push rejected: non-fast-forward", storage.FailureSyncRemoteAhead},
		{"history diverged", "cannot find common ancestor", storage.FailureHistoryDiverged},
		{"remote data missing", "Blob not found: abc", storage.FailureRemoteDataMissing},
		{"timeout", "dial tcp: i/o timeout", storage.FailureRemoteUnreachable},
		{"dangling", "dangling chunk reference: abc", storage.FailureDanglingReference},
		{"unknown", "operation returned an undocumented problem", storage.FailureOperationFailedUnknown},
		{"generic lock is unknown", "lock could be useful here", storage.FailureOperationFailedUnknown},
		{"generic timeout is unknown", "timeout", storage.FailureOperationFailedUnknown},
		{"generic unknown database is unknown", "unknown database", storage.FailureOperationFailedUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyFailureCode(errors.New(tt.err)); got != tt.want {
				t.Errorf("ClassifyFailureCode(%q) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
