#!/usr/bin/env bash
# Run one manifest-driven shard of the compiled Embedded Dolt storage suite.

set -euo pipefail

SHARD_NUMBER="${1:?usage: $0 <shard_number> <total_shards> [test-binary args...]}"
TOTAL_SHARDS="${2:?usage: $0 <shard_number> <total_shards> [test-binary args...]}"
shift 2

if ! [[ "$SHARD_NUMBER" =~ ^[0-9]+$ && "$TOTAL_SHARDS" =~ ^[0-9]+$ ]] || (( TOTAL_SHARDS < 1 || SHARD_NUMBER < 1 || SHARD_NUMBER > TOTAL_SHARDS )); then
  echo "Invalid shard ${SHARD_NUMBER}/${TOTAL_SHARDS}" >&2
  exit 1
fi

STORAGE_BINARY="${BEADS_TEST_EMBEDDED_TEST_BINARY:-/tmp/embeddeddolt-test}"
MANIFEST="${BEADS_TEST_SHARD_MANIFEST:-.github/scripts/embedded-storage-test-shards.txt}"
ARTIFACT_DIR="${BEADS_TEST_SHARD_ARTIFACT_DIR:-artifacts}"
ARTIFACT_PREFIX="embedded-storage-shard-${SHARD_NUMBER}-of-${TOTAL_SHARDS}"
SELECTED_FILE="${ARTIFACT_DIR}/${ARTIFACT_PREFIX}-selected.txt"
TIMING_FILE="${ARTIFACT_DIR}/${ARTIFACT_PREFIX}-timing.tsv"
SUMMARY_FILE="${ARTIFACT_DIR}/${ARTIFACT_PREFIX}-summary.txt"
LOG_FILE="${ARTIFACT_DIR}/${ARTIFACT_PREFIX}.log"

if [[ ! -x "$STORAGE_BINARY" ]]; then
  echo "Embedded storage test binary is missing or not executable: $STORAGE_BINARY" >&2
  exit 1
fi
if [[ ! -f "$MANIFEST" ]]; then
  echo "Embedded storage shard manifest is missing: $MANIFEST" >&2
  exit 1
fi

if ! ALL_TESTS_RAW=$("$STORAGE_BINARY" -test.list '^Test'); then
  echo "Embedded storage test inventory failed: $STORAGE_BINARY -test.list '^Test'" >&2
  exit 1
fi
ALL_TESTS=$(printf '%s\n' "$ALL_TESTS_RAW" | grep -E '^Test[A-Za-z0-9_]+$' | sort -u || true)
ALL_TESTS=$(printf '%s\n' "$ALL_TESTS" | grep -Ev '^(TestConformance|TestHelperProcess)$' || true)
if [[ -z "$ALL_TESTS" ]]; then
  echo "Embedded storage test inventory is empty" >&2
  exit 1
fi

ALL_TEST_NAMES=()
while IFS= read -r name; do
  ALL_TEST_NAMES+=("$name")
done <<< "$ALL_TESTS"

contains_exact_test() {
  local needle="$1"
  local candidate
  shift
  for candidate in "$@"; do
    if [[ "$candidate" == "$needle" ]]; then
      return 0
    fi
  done
  return 1
}

MANIFEST_TEST_NAMES=()
MANIFEST_SHARD_NUMBERS=()
manifest_shard_for_test() {
  local test_name="$1"
  local index
  for index in "${!MANIFEST_TEST_NAMES[@]}"; do
    if [[ "${MANIFEST_TEST_NAMES[$index]}" == "$test_name" ]]; then
      printf '%s\n' "${MANIFEST_SHARD_NUMBERS[$index]}"
      return 0
    fi
  done
  return 1
}

while IFS= read -r line || [[ -n "$line" ]]; do
  line="${line%%#*}"
  read -r manifest_total manifest_shard test_name extra <<< "$line"
  if [[ -z "${manifest_total:-}" ]]; then
    continue
  fi
  if [[ -n "${extra:-}" || ! "$manifest_total" =~ ^[0-9]+$ || ! "$manifest_shard" =~ ^[0-9]+$ || ! "$test_name" =~ ^Test[A-Za-z0-9_]+$ ]] || (( manifest_total < 1 )); then
    echo "Invalid shard manifest line: $line" >&2
    exit 1
  fi
  if (( manifest_total != TOTAL_SHARDS )); then
    continue
  fi
  if (( manifest_shard < 1 || manifest_shard > TOTAL_SHARDS )); then
    echo "Invalid shard ${manifest_shard}/${manifest_total} for $test_name in $MANIFEST" >&2
    exit 1
  fi
  if [[ "$test_name" == "TestConformance" || "$test_name" == "TestHelperProcess" ]]; then
    echo "Special-owner test is not allowed in storage shard manifest: $test_name" >&2
    exit 1
  fi
  if manifest_shard_for_test "$test_name" >/dev/null; then
    echo "Duplicate shard manifest entry for $test_name in $MANIFEST" >&2
    exit 1
  fi
  MANIFEST_TEST_NAMES+=("$test_name")
  MANIFEST_SHARD_NUMBERS+=("$manifest_shard")
done < "$MANIFEST"

for name in "${MANIFEST_TEST_NAMES[@]}"; do
  if ! contains_exact_test "$name" "${ALL_TEST_NAMES[@]}"; then
    echo "Shard manifest entry does not match the compiled test inventory: $name" >&2
    exit 1
  fi
done

SHARD_TESTS=()
SHARD_SOURCES=()
MANIFEST_COUNT=0
FALLBACK_COUNT=0
for name in "${ALL_TEST_NAMES[@]}"; do
  source="manifest"
  if ! assigned_shard=$(manifest_shard_for_test "$name"); then
    hash=$(printf '%s' "$name" | cksum | awk '{print $1}')
    assigned_shard=$(( (hash % TOTAL_SHARDS) + 1 ))
    source="fallback"
  fi
  if (( assigned_shard == SHARD_NUMBER )); then
    SHARD_TESTS+=("$name")
    SHARD_SOURCES+=("$source")
    if [[ "$source" == "manifest" ]]; then
      MANIFEST_COUNT=$((MANIFEST_COUNT + 1))
    else
      FALLBACK_COUNT=$((FALLBACK_COUNT + 1))
      echo "Warning: $name is not in $MANIFEST; assigning it to shard ${assigned_shard}/${TOTAL_SHARDS} by cksum fallback" >&2
    fi
  fi
done

if (( ${#SHARD_TESTS[@]} == 0 )); then
  echo "Shard ${SHARD_NUMBER}/${TOTAL_SHARDS} has no selected tests" >&2
  exit 1
fi

mkdir -p "$ARTIFACT_DIR"
: > "$SELECTED_FILE"
for index in "${!SHARD_TESTS[@]}"; do
  printf '%s %s %s\n' "${SHARD_TESTS[$index]}" "${SHARD_SOURCES[$index]}" "$SHARD_NUMBER" >> "$SELECTED_FILE"
done
: > "$LOG_FILE"
printf 'test_name\tduration_seconds\tstatus\n' > "$TIMING_FILE"

RUN_REGEX="^($(IFS='|'; echo "${SHARD_TESTS[*]}"))$"
if [[ "${BEADS_TEST_SHARD_LIST_ONLY:-}" == "1" ]]; then
  cat > "$SUMMARY_FILE" <<EOF
wall_seconds=0
selected_count=${#SHARD_TESTS[@]}
manifest_count=$MANIFEST_COUNT
fallback_count=$FALLBACK_COUNT
process_exit_status=0
timing_parse=unavailable
list_only=1
EOF
  exit 0
fi

start_seconds=$(date +%s)
set +e
"$STORAGE_BINARY" -test.v -test.count=1 -test.timeout=20m -test.run "$RUN_REGEX" "$@" 2>&1 | tee "$LOG_FILE"
TEST_STATUS=${PIPESTATUS[0]}
set -e
end_seconds=$(date +%s)

sed -nE 's/.*--- (PASS|FAIL): (Test[A-Za-z0-9_]+) \(([0-9.]+)s\).*/\2\t\3\t\1/p' "$LOG_FILE" >> "$TIMING_FILE"
if [[ $(wc -l < "$TIMING_FILE") -gt 1 ]]; then
  TIMING_PARSE="available"
else
  TIMING_PARSE="unavailable"
fi
cat > "$SUMMARY_FILE" <<EOF
wall_seconds=$((end_seconds - start_seconds))
selected_count=${#SHARD_TESTS[@]}
manifest_count=$MANIFEST_COUNT
fallback_count=$FALLBACK_COUNT
process_exit_status=$TEST_STATUS
timing_parse=$TIMING_PARSE
EOF

exit "$TEST_STATUS"
