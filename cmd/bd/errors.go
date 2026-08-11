package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	storagedolt "github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/storage/schema"
)

type exitError struct {
	Code int
}

func (e *exitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

func exitCodeFromError(err error) (int, bool) {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.Code, true
	}
	return 0, false
}

func activeWorkspaceNotFoundError() string {
	return "no active beads workspace found"
}

func activeWorkspaceNotFoundMessage() string {
	return "No active beads workspace found."
}

func diagHint() string {
	return workspaceDiagHint(true)
}

func whereDiagHint() string {
	return workspaceDiagHint(false)
}

func workspaceDiagHint(includeWhere bool) string {
	if includeWhere {
		if !usesSQLServer() {
			return "run 'bd where' to inspect the resolved workspace, or 'bd init' to create a new database"
		}
		return "run 'bd where' to inspect the resolved workspace, run 'bd doctor' to diagnose, or 'bd init' to create a new database"
	}
	if !usesSQLServer() {
		return "check BEADS_DIR/worktree setup, or run 'bd init' to create a new database"
	}
	return "check BEADS_DIR/worktree setup, run 'bd doctor' to diagnose, or run 'bd init' to create a new database"
}

func buildJSONError(message, hint string) interface{} {
	inner := map[string]interface{}{"error": message}
	if hint != "" {
		inner["hint"] = hint
	}
	if jsonEnvelopeEnabled() {
		return map[string]interface{}{"schema_version": JSONSchemaVersion, "data": inner}
	}
	inner["schema_version"] = JSONSchemaVersion
	return inner
}

var urlInMessage = regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s]+`)

// redactURLCredentials removes URL userinfo and query strings before an error
// is projected into JSON. Query values commonly carry credentials, and neither
// a user name nor a token is a stable recovery fact.
func redactURLCredentials(message string) string {
	return urlInMessage.ReplaceAllStringFunc(message, func(raw string) string {
		trimmed := strings.TrimRight(raw, ".,;:)")
		suffix := strings.TrimPrefix(raw, trimmed)
		u, err := url.Parse(trimmed)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return raw
		}
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		return u.String() + suffix
	})
}

// buildJSONClassifiedError extends the established error payload at the scoped
// recovery producer boundary. An empty code preserves the legacy helper's
// shape for all callers outside that boundary.
func buildJSONClassifiedError(message, hint string, failureCode storage.FailureCode, evidence map[string]interface{}) interface{} {
	inner := map[string]interface{}{
		"error": redactURLCredentials(message),
	}
	if hint != "" {
		inner["hint"] = redactURLCredentials(hint)
	}
	if failureCode != "" {
		inner["failure_code"] = failureCode
	}
	if len(evidence) > 0 {
		inner["evidence"] = evidence
	}
	if jsonEnvelopeEnabled() {
		return map[string]interface{}{
			"schema_version": JSONSchemaVersion,
			"data":           inner,
		}
	}
	inner["schema_version"] = JSONSchemaVersion
	return inner
}

func jsonStderrClassifiedError(message, hint string, failureCode storage.FailureCode, evidence map[string]interface{}) {
	encoder := json.NewEncoder(os.Stderr)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(buildJSONClassifiedError(message, hint, failureCode, evidence))
}

func buildJSONWarning(message string, failureCode storage.FailureCode, evidence map[string]interface{}) interface{} {
	inner := map[string]interface{}{
		"warning": redactURLCredentials(message),
	}
	if failureCode != "" {
		inner["failure_code"] = failureCode
	}
	if len(evidence) > 0 {
		inner["evidence"] = evidence
	}
	if jsonEnvelopeEnabled() {
		return map[string]interface{}{"schema_version": JSONSchemaVersion, "data": inner}
	}
	inner["schema_version"] = JSONSchemaVersion
	return inner
}

func jsonStderrWarning(message string, failureCode storage.FailureCode, evidence map[string]interface{}) {
	encoder := json.NewEncoder(os.Stderr)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(buildJSONWarning(message, failureCode, evidence))
}

func jsonErrorPayload(outer interface{}) map[string]interface{} {
	payload, _ := outer.(map[string]interface{})
	if payload == nil {
		return nil
	}
	if data, ok := payload["data"].(map[string]interface{}); ok {
		return data
	}
	return payload
}

func databaseOpenFailureCode(err error) storage.FailureCode {
	var skewErr *schema.SchemaSkewError
	var gateErr *schema.RemoteMigrateGateError
	if errors.As(err, &skewErr) || errors.As(err, &gateErr) {
		return storage.FailureSchemaMigrationRequired
	}
	if code, ok := storage.CodeOf(err); ok {
		return code
	}
	code := storagedolt.ClassifyFailureCode(err)
	if code == storage.FailureOperationFailedUnknown {
		return storage.FailureDatabaseOpenFailed
	}
	return code
}

func reportDatabaseOpenFailure(err error) {
	jsonStderrClassifiedError(
		fmt.Sprintf("failed to open database: %v", err),
		"",
		databaseOpenFailureCode(err),
		map[string]interface{}{"operation": "database_open"},
	)
}

func jsonStderrError(message, hint string) {
	encoder := json.NewEncoder(os.Stderr)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(buildJSONError(message, hint))
}

func jsonStdoutError(message, hint string) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(buildJSONError(message, hint))
}

func HandleError(format string, args ...interface{}) error {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	return &exitError{Code: 1}
}

func HandleErrorRespectJSON(format string, args ...interface{}) error {
	if jsonOutput {
		jsonStdoutError(fmt.Sprintf(format, args...), "")
		return &exitError{Code: 1}
	}
	return HandleError(format, args...)
}

func HandleErrorWithHint(message, hint string) error {
	if jsonOutput {
		jsonStderrError(message, hint)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
		fmt.Fprintf(os.Stderr, "Hint: %s\n", hint)
	}
	return &exitError{Code: 1}
}

func HandleErrorWithHintRespectJSON(message, hint string) error {
	if jsonOutput {
		jsonStdoutError(message, hint)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
		fmt.Fprintf(os.Stderr, "Hint: %s\n", hint)
	}
	return &exitError{Code: 1}
}

func SilentExit() error {
	return &exitError{Code: 1}
}

// FatalError writes an error message to stderr (structured JSON when --json is
// set) and exits with code 1.
//
// It is retained ONLY for the proxied-server code paths, which run outside
// cobra's RunE error-return convention; every RunE-converted command uses
// HandleError and friends instead. Because FatalError calls os.Exit it bypasses
// the per-command deferred metrics CloseEventAndAdd and main()'s
// metrics.Global().Close()/MaybeSpawnFlusher, so a command that exits through a
// proxied-server FatalError* path records no usage event. That telemetry gap is
// latent today: proxied-server mode cannot be entered ("bd init --proxied-server"
// is rejected as "not yet implemented", see init.go), so usesProxiedServer() is
// never true and these paths never run (verified by
// TestInitProxiedServerRejectedKeepsMetricsGapLatent). When proxied-server mode
// is completed, convert these helpers to return errors up through RunE — like
// HandleError — so the deferred metrics close/flush is preserved.
func FatalError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if jsonOutput {
		jsonStderrError(msg, "")
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	}
	os.Exit(1)
}

// FatalErrorRespectJSON writes an error message and exits with code 1. If
// --json is set, outputs structured JSON to stdout; otherwise plain text to
// stderr.
func FatalErrorRespectJSON(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if jsonOutput {
		jsonStdoutError(msg, "")
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	}
	os.Exit(1)
}

// FatalErrorWithHintRespectJSON writes an error message with a hint and exits.
// If --json is set, emits structured JSON to stdout so callers can parse it.
func FatalErrorWithHintRespectJSON(message, hint string) {
	if jsonOutput {
		jsonStdoutError(message, hint)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
		fmt.Fprintf(os.Stderr, "Hint: %s\n", hint)
	}
	os.Exit(1)
}

// FatalErrorWithHint writes an error message with a hint to stderr and exits.
func FatalErrorWithHint(message, hint string) {
	if jsonOutput {
		jsonStderrError(message, hint)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
		fmt.Fprintf(os.Stderr, "Hint: %s\n", hint)
	}
	os.Exit(1)
}

func WarnError(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Warning: "+format+"\n", args...)
}

// CheckReadonly aborts the command when bd is running in read-only mode (the
// worker-sandbox posture, see readonlyMode). Like the proxied-server FatalError*
// family above, it exits via os.Exit and so cannot run the per-command deferred
// CloseEventAndAdd — a command blocked here records no cli_command event of its
// own (it never actually ran). It does flush metrics first, so events already
// queued earlier in this run are still written and scheduled for upload rather
// than stranded until the next clean exit.
func CheckReadonly(operation string) {
	if readonlyMode {
		fmt.Fprintf(os.Stderr, "Error: operation '%s' is not allowed in read-only mode\n", operation)
		metrics.CloseAndFlush()
		os.Exit(1)
	}
}
