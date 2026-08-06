package storage

// LocalStoreLocation identifies the on-disk directory that holds one database
// of a backend's local store. It is produced by
// LocalStoreRecoverer.LocateLocalStore and consumed by
// LocalStoreRecoverer.QuarantineLocalStore; callers pass it along rather than
// deriving paths themselves, which is what keeps the on-disk layout behind the
// interface.
type LocalStoreLocation struct {
	// DataDir is the directory the backend keeps its databases in, exactly as
	// it was passed to LocateLocalStore.
	DataDir string
	// Database is the database name that was looked up.
	Database string

	// Path is the directory that holds this database's data, and the unit a
	// quarantine moves. Empty when Found is false.
	Path string
	// Found reports whether a store for Database exists under DataDir.
	Found bool
	// Reason explains a Found == false verdict. Empty when Found is true.
	Reason string
}

// LocalStoreRecoverer is the storage-boundary capability for repairing a local
// on-disk store that cannot be opened.
//
// Why it is not part of Storage: every method on Storage presupposes a working
// connection, and a store that fails to open cannot answer queries. Recovery
// therefore has to be reachable before — and instead of — an open, so this is a
// free-standing capability obtained from the backend package rather than from
// an opened store handle.
//
// Ownership boundary (docs/PROJECT_CHARTER.md "Storage Boundary"):
//
//   - The backend owns the layout. Which directory under a data dir is a
//     database, and what marks a directory as one, is engine knowledge and
//     lives behind this interface. Callers name a data directory and a database
//     and never walk, pattern-match, or reconstruct the layout themselves.
//   - The caller owns the policy. Whether a repair is warranted, and where a
//     quarantined store belongs, are product decisions; the interface takes the
//     destination as a parameter instead of inventing one.
//   - The engine's private state directory is opaque *to callers*, and to
//     LocateLocalStore and QuarantineLocalStore: those may test for its
//     presence to recognize a database directory, but never open, read, parse,
//     list, or descend into it. A store that failed to open is by definition
//     one whose internal state cannot be trusted to parse, so reading it from
//     a beads package would reintroduce exactly the crash-recovery workaround
//     the charter forbids. InspectLocalStoreDamage is the single deliberate
//     exception: judging damage requires engine knowledge, which is why it is
//     expressed as a backend method here rather than performed by the caller.
//   - Nothing here deletes data. The only mutation is a move to a
//     caller-supplied destination, so a mistaken diagnosis stays reversible.
type LocalStoreRecoverer interface {
	// LocateLocalStore reports the directory holding database under dataDir.
	//
	// A missing data directory, a missing database directory, and a directory
	// that carries no engine state are all Found == false with a populated
	// Reason, not errors: they are ordinary states of a broken rig. An error is
	// returned only for a malformed request (such as an empty database name) or
	// an I/O failure that leaves the verdict genuinely unknown.
	//
	// LocateLocalStore never modifies anything.
	LocateLocalStore(dataDir, database string) (LocalStoreLocation, error)

	// InspectLocalStoreDamage reports the store directories under beadsDir the
	// backend judges structurally damaged beyond what a retry can clear, empty
	// when it finds none.
	//
	// It exists so a caller never has to read engine state to decide whether a
	// failed open is corruption: the open error alone under-reports, because a
	// store can be damaged in a way that surfaces as a failed server start
	// rather than a corruption message. Widening the interface is the charter's
	// prescribed answer to exactly that ("If the current storage interface
	// cannot express a needed operation, widen the interface"), and it keeps
	// the engine knowledge on the backend side of the boundary.
	//
	// Detection only — like the rest of this capability it never mutates, and
	// an unreadable or absent rig is no damage rather than an error.
	InspectLocalStoreDamage(beadsDir string) ([]string, error)

	// QuarantineLocalStore moves the located store directory to destination,
	// creating the destination's parent as needed.
	//
	// It refuses a location that was never found, a destination that already
	// exists, and any destination inside the store being moved or inside
	// loc.DataDir — a store parked inside the data directory is still scanned
	// as a database by the engine, so such a "backup" would keep the corruption
	// live. Callers that need the quarantine outside a particular tree must
	// still choose such a destination themselves; this method only enforces the
	// invariants the storage layer owns.
	//
	// On refusal nothing is moved. The move is a rename, so it is atomic within
	// a filesystem and fails loudly across filesystems rather than leaving a
	// partial copy.
	QuarantineLocalStore(loc LocalStoreLocation, destination string) error
}
