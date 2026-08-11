#!/usr/bin/env bash
# Shared command timing helpers for CI wrappers.

if [[ -n "${BEADS_CI_TIMING_SH_LOADED:-}" ]]; then
    return 0
fi
BEADS_CI_TIMING_SH_LOADED=1

ci_markdown_escape() {
    local value="$1"
    value="${value//|/\\|}"
    printf '%s' "$value"
}

ci_timing_write_summary() {
    local label="$1"
    local duration="$2"
    local status="$3"

    [[ -n "${GITHUB_STEP_SUMMARY:-}" ]] || return 0

    if [[ -z "${BEADS_CI_TIMING_SUMMARY_HEADER_WRITTEN:-}" ]]; then
        {
            printf '### CI command timings\n\n'
            printf '| Command | Duration | Status |\n'
            printf '|---|---:|---:|\n'
        } >>"$GITHUB_STEP_SUMMARY"
        BEADS_CI_TIMING_SUMMARY_HEADER_WRITTEN=1
    fi

    printf '| %s | %ss | %s |\n' \
        "$(ci_markdown_escape "$label")" \
        "$duration" \
        "$status" >>"$GITHUB_STEP_SUMMARY"
}

ci_time() {
    if [[ $# -lt 3 || "$2" != "--" ]]; then
        printf 'usage: ci_time <label> -- <command> [args...]\n' >&2
        return 2
    fi

    local label="$1"
    shift 2

    local start end duration status errexit_was_set
    errexit_was_set=0
    case "$-" in
        *e*) errexit_was_set=1 ;;
    esac

    printf '==> %s\n' "$label"
    start="$(date +%s)"

    set +e
    "$@"
    status=$?
    if [[ "$errexit_was_set" -eq 1 ]]; then
        set -e
    else
        set +e
    fi

    end="$(date +%s)"
    duration=$((end - start))

    if [[ "$status" -eq 0 ]]; then
        printf '<== %s succeeded in %ss\n' "$label" "$duration"
    else
        printf '<== %s failed after %ss with exit code %s\n' "$label" "$duration" "$status" >&2
    fi

    ci_timing_write_summary "$label" "$duration" "$status"
    return "$status"
}

ci_time_accumulate() {
    if [[ $# -lt 4 || "$3" != "--" ]]; then
        printf 'usage: ci_time_accumulate <status-var> <label> -- <command> [args...]\n' >&2
        return 2
    fi

    local status_var="$1"
    local label="$2"
    local command_status=0
    local aggregate_status

    if [[ ! "$status_var" =~ ^[a-zA-Z_][a-zA-Z0-9_]*$ ]]; then
        printf 'ci_time_accumulate: invalid status variable %q\n' "$status_var" >&2
        return 2
    fi

    aggregate_status="${!status_var:-0}"
    if [[ ! "$aggregate_status" =~ ^[0-9]+$ ]]; then
        printf 'ci_time_accumulate: status variable %s must contain a non-negative integer\n' "$status_var" >&2
        return 2
    fi

    shift 3
    ci_time "$label" -- "$@" || command_status=$?

    if [[ "$aggregate_status" -eq 0 && "$command_status" -ne 0 ]]; then
        printf -v "$status_var" '%s' "$command_status"
    fi

    return 0
}
