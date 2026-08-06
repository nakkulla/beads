package main

import (
	"context"
	"errors"
	"testing"
)

// A2: init must merge the fleet "resolved" workflow status into status.custom
// without losing values a rig already carries.
func TestInitMergeCustomStatusValue(t *testing.T) {
	tests := []struct {
		name        string
		existing    string
		add         string
		want        string
		wantChanged bool
		wantErr     bool
	}{
		{
			name:        "empty existing seeds the token",
			existing:    "",
			add:         "resolved",
			want:        "resolved",
			wantChanged: true,
		},
		{
			name:        "whitespace-only existing seeds the token",
			existing:    "  \t ",
			add:         "resolved",
			want:        "resolved",
			wantChanged: true,
		},
		{
			name:        "absent token is appended after existing values",
			existing:    "review,testing",
			add:         "resolved",
			want:        "review,testing,resolved",
			wantChanged: true,
		},
		{
			name:        "padded existing is trimmed before the append",
			existing:    "  review  ",
			add:         "resolved",
			want:        "review,resolved",
			wantChanged: true,
		},
		{
			name:     "already-present token is a no-op",
			existing: "resolved",
			add:      "resolved",
			want:     "resolved",
		},
		{
			name:     "already-present token among others is a no-op",
			existing: "review,resolved,testing",
			add:      "resolved",
			want:     "review,resolved,testing",
		},
		{
			name:     "categorized existing token counts as present",
			existing: "review:active,resolved:done",
			add:      "resolved",
			want:     "review:active,resolved:done",
		},
		{
			name:     "inner whitespace does not hide an existing token",
			existing: " review , resolved ",
			add:      "resolved",
			want:     " review , resolved ",
		},
		{
			name:     "case-insensitive match against an existing token is a no-op",
			existing: "resolved",
			add:      "RESOLVED",
			want:     "resolved",
		},
		{
			name:     "uppercase token cannot be seeded (parser rejects it)",
			existing: "",
			add:      "RESOLVED",
			wantErr:  true,
		},
		{
			name:     "empty token is rejected",
			existing: "review",
			add:      "  ",
			wantErr:  true,
		},
		{
			name:     "unparseable existing value is rejected instead of clobbered",
			existing: "not a status",
			add:      "resolved",
			wantErr:  true,
		},
		{
			name:     "duplicate existing entries are rejected instead of clobbered",
			existing: "review,review",
			add:      "resolved",
			wantErr:  true,
		},
		{
			name:     "token colliding with a built-in status is rejected",
			existing: "review",
			add:      "closed",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := mergeCustomStatusValue(tt.existing, tt.add)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("mergeCustomStatusValue(%q, %q) = (%q, %v, nil), want error", tt.existing, tt.add, got, changed)
				}
				return
			}
			if err != nil {
				t.Fatalf("mergeCustomStatusValue(%q, %q) returned unexpected error: %v", tt.existing, tt.add, err)
			}
			if got != tt.want {
				t.Errorf("merged value = %q, want %q", got, tt.want)
			}
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
		})
	}
}

// fakeCustomStatusStore records the status.custom reads/writes the seeding
// wiring performs, so the gate and the read-then-merge contract are testable
// without a live Dolt server.
type fakeCustomStatusStore struct {
	value    string
	getErr   error
	setErr   error
	getCalls int
	setCalls []string
}

func (f *fakeCustomStatusStore) GetConfig(_ context.Context, key string) (string, error) {
	if key != "status.custom" {
		return "", nil
	}
	f.getCalls++
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.value, nil
}

func (f *fakeCustomStatusStore) SetConfig(_ context.Context, key, value string) error {
	if key != "status.custom" {
		return nil
	}
	if f.setErr != nil {
		return f.setErr
	}
	f.setCalls = append(f.setCalls, value)
	f.value = value
	return nil
}

func TestInitSeedServerModeResolvedStatus(t *testing.T) {
	sentinelGet := errors.New("boom-get")
	sentinelSet := errors.New("boom-set")

	tests := []struct {
		name           string
		initServerMode bool
		store          *fakeCustomStatusStore
		wantSeeded     bool
		wantErr        bool
		wantGetCalls   int
		wantSetCalls   []string
	}{
		{
			name:           "embedded mode never touches the config",
			initServerMode: false,
			store:          &fakeCustomStatusStore{},
			wantGetCalls:   0,
		},
		{
			name:           "server mode seeds an empty value",
			initServerMode: true,
			store:          &fakeCustomStatusStore{},
			wantSeeded:     true,
			wantGetCalls:   1,
			wantSetCalls:   []string{"resolved"},
		},
		{
			name:           "server mode preserves existing custom statuses",
			initServerMode: true,
			store:          &fakeCustomStatusStore{value: "awaiting_review,awaiting_testing"},
			wantSeeded:     true,
			wantGetCalls:   1,
			wantSetCalls:   []string{"awaiting_review,awaiting_testing,resolved"},
		},
		{
			name:           "server mode is idempotent when the token exists",
			initServerMode: true,
			store:          &fakeCustomStatusStore{value: "review,resolved"},
			wantGetCalls:   1,
		},
		{
			name:           "read failure surfaces and writes nothing",
			initServerMode: true,
			store:          &fakeCustomStatusStore{getErr: sentinelGet},
			wantErr:        true,
			wantGetCalls:   1,
		},
		{
			name:           "write failure surfaces",
			initServerMode: true,
			store:          &fakeCustomStatusStore{setErr: sentinelSet},
			wantErr:        true,
			wantGetCalls:   1,
		},
		{
			name:           "unparseable existing value is not clobbered",
			initServerMode: true,
			store:          &fakeCustomStatusStore{value: "not a status"},
			wantErr:        true,
			wantGetCalls:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seeded, err := seedServerModeResolvedStatus(context.Background(), tt.store, tt.initServerMode)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("seedServerModeResolvedStatus() = (%v, nil), want error", seeded)
				}
			} else if err != nil {
				t.Fatalf("seedServerModeResolvedStatus() returned unexpected error: %v", err)
			}
			if seeded != tt.wantSeeded {
				t.Errorf("seeded = %v, want %v", seeded, tt.wantSeeded)
			}
			if tt.store.getCalls != tt.wantGetCalls {
				t.Errorf("GetConfig calls = %d, want %d", tt.store.getCalls, tt.wantGetCalls)
			}
			if len(tt.store.setCalls) != len(tt.wantSetCalls) {
				t.Fatalf("SetConfig calls = %v, want %v", tt.store.setCalls, tt.wantSetCalls)
			}
			for i, want := range tt.wantSetCalls {
				if tt.store.setCalls[i] != want {
					t.Errorf("SetConfig call %d = %q, want %q", i, tt.store.setCalls[i], want)
				}
			}
		})
	}
}

// Re-running the seeding must not accumulate duplicate tokens.
func TestInitSeedServerModeResolvedStatusIsIdempotent(t *testing.T) {
	fake := &fakeCustomStatusStore{value: "awaiting_review"}

	seeded, err := seedServerModeResolvedStatus(context.Background(), fake, true)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if !seeded {
		t.Fatalf("first seed did not write")
	}

	seeded, err = seedServerModeResolvedStatus(context.Background(), fake, true)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if seeded {
		t.Fatalf("second seed wrote again, want no-op")
	}
	if fake.value != "awaiting_review,resolved" {
		t.Fatalf("status.custom = %q, want %q", fake.value, "awaiting_review,resolved")
	}
}
