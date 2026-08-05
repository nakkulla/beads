package fix

import (
	"strings"

	"github.com/steveyegge/beads/internal/doltserver"
)

// StoreOpenClass classifies a failed local Dolt store open.
type StoreOpenClass string

const (
	// StoreOpenClassNone means no open failure was recorded — the store opened,
	// or no open was attempted.
	StoreOpenClassNone StoreOpenClass = ""

	// StoreOpenClassCorrupt means the failure carries a signature that only
	// appears when the on-disk Dolt data is damaged. Retrying cannot clear it;
	// recovery needs the store to be replaced from a remote.
	StoreOpenClassCorrupt StoreOpenClass = "corrupt"

	// StoreOpenClassTransient means the failure is a connectivity, lifecycle,
	// permission, or configuration problem that a retry (or starting the
	// server) can clear. This is also the default for anything unrecognized.
	StoreOpenClassTransient StoreOpenClass = "transient"
)

// storeCorruptionSignatures are the substrings that appear in a store-open
// error only when the local Dolt data itself is damaged. Every entry is a
// literal bd or Dolt emits; there is no pre-existing classifier for open
// errors to reuse (internal/storage/dberrors only classifies MySQL 1146, and
// internal/storage/dolt/errors.go's sentinels cover query/transaction/push
// paths, not open), so this list is deliberately narrow:
//
//   - "Corrupt manifest with no recoverable data" — doltserver.Start wraps a
//     failed start with this once detectCorruptManifest confirms the GH#3290
//     shape (internal/doltserver/doltserver.go).
//   - "Dolt journal corruption detected in" — the first line of
//     corruptJournalRecoveryHint, attached when the server starts but never
//     becomes ready and the log shows Dolt's journal-corruption signature
//     (internal/doltserver/manifest_recovery.go, GH#2559).
//   - "root hash doesn't exist" / "corrupted journal" — the raw Dolt log lines
//     the two constants above are matched against
//     (corruptManifestSignature / corruptJournalSignature). Included because a
//     caller may surface the log line itself rather than bd's wrapper.
//
// Classification is intentionally asymmetric: only a positive corruption
// signature yields StoreOpenClassCorrupt, everything else falls through to
// transient. Calling real corruption transient costs a retry; calling a
// transient failure corruption would aim a destructive re-clone at a healthy
// store.
var storeCorruptionSignatures = []string{
	"Corrupt manifest with no recoverable data",
	"Dolt journal corruption detected in",
	"root hash doesn't exist",
	"corrupted journal",
}

// corruptManifestStructuralSignature is reported when the on-disk scan, not the
// error text, is what proved corruption.
const corruptManifestStructuralSignature = "corrupt manifest with no recoverable data (on-disk scan)"

// ClassifyStoreOpenError classifies a store-open failure and returns the
// corruption signature that decided a corrupt verdict ("" for every other
// class). Text matching is used because the open path collapses Dolt's server
// startup diagnostics into a wrapped error string — no typed sentinel survives
// it.
func ClassifyStoreOpenError(err error) (StoreOpenClass, string) {
	if err == nil {
		return StoreOpenClassNone, ""
	}
	msg := err.Error()
	for _, signature := range storeCorruptionSignatures {
		if strings.Contains(msg, signature) {
			return StoreOpenClassCorrupt, signature
		}
	}
	return StoreOpenClassTransient, ""
}

// LocalStoreHealthReport describes a rig's local store-open failure and whether
// a re-clone source exists for it.
type LocalStoreHealthReport struct {
	// OpenErr is the preserved store-open error, nil when the store opened or
	// no open was attempted.
	OpenErr error
	// Class classifies OpenErr.
	Class StoreOpenClass
	// Signature is the corruption marker behind Class == StoreOpenClassCorrupt.
	Signature string
	// CorruptDirs are the .dolt/noms directories the on-disk scan found
	// structurally corrupt, if any.
	CorruptDirs []string

	// Remote is the effective sync remote, "" when none is configured, and
	// RemoteKey the config.yaml key it came from.
	Remote    string
	RemoteKey string
	// RemoteNormalized is Remote in Dolt's remote form.
	RemoteNormalized string
	// RemoteUsable reports whether the rig has a re-clone source at all.
	//
	// It is exactly "a remote is configured": GH#3339 established that bd
	// cannot judge a configured value invalid from its shape, because a plain
	// http(s) URL can be a self-hosted remotesapi endpoint. Shape complaints
	// belong to the Sync Remote Shape check; this field only answers whether
	// there is anything to re-clone from.
	RemoteUsable bool

	// ServerMode reports whether the rig talks to an external Dolt server, in
	// which case the local .beads/dolt tree is not the recovery target.
	ServerMode bool
}

// InspectLocalStoreHealth reports why a rig's shared store failed to open and
// whether a re-clone source is configured. Read-only: it classifies the
// preserved error and scans the dolt directory, and never modifies anything.
//
// Remote resolution reuses InspectSyncRemoteShape so the key precedence
// (sync.remote, then the deprecated sync.git-remote) and the shape-tolerant
// config read stay defined in one place.
func InspectLocalStoreHealth(beadsDir string, openErr error) LocalStoreHealthReport {
	report := LocalStoreHealthReport{OpenErr: openErr}
	report.Class, report.Signature = ClassifyStoreOpenError(openErr)

	shape := InspectSyncRemoteShape(beadsDir)
	report.Remote = shape.Remote
	report.RemoteKey = shape.SourceKey
	report.RemoteNormalized = shape.Normalized
	report.RemoteUsable = shape.Remote != ""
	report.ServerMode = shape.ServerMode

	if openErr == nil {
		return report
	}

	// Corroborate (or establish) corruption from disk. DetectCorruptManifest
	// is detection-only by contract (bd-6dnrw.6) and only fires when the server
	// log carries the signature AND the databases hold no recoverable data, so
	// a positive result on top of a failed open is strong evidence.
	if dirs, err := doltserver.DetectCorruptManifest(beadsDir); err == nil && len(dirs) > 0 {
		report.CorruptDirs = dirs
		if report.Class != StoreOpenClassCorrupt {
			report.Class = StoreOpenClassCorrupt
			report.Signature = corruptManifestStructuralSignature
		}
	}

	return report
}
