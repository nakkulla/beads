#!/bin/sh
set -eu

fail() {
  printf '%s\n' "bdui managed deploy: $*" >&2
  exit 1
}

need() {
  name=$1
  eval "value=\${$name-}"
  [ -n "$value" ] || fail "missing $name"
}

is_sha() {
  printf '%s' "$1" | grep -Eq '^[0-9A-Fa-f]{40}$'
}

sha256_file() {
  "$PYTHON" -c 'import hashlib, sys; print(hashlib.sha256(open(sys.argv[1], "rb").read()).hexdigest())' "$1"
}

sha256_text() {
  "$PYTHON" -c 'import hashlib, sys; print(hashlib.sha256(sys.argv[1].encode()).hexdigest())' "$1"
}

safe_component() {
  safe=$(printf '%s' "$1" | LC_ALL=C sed 's/[^A-Za-z0-9._-]/_/g')
  [ -n "$safe" ] || safe=$2
  printf '%s' "$safe"
}

assert_safe_existing_dirs() {
  path=$1
  case "$path" in
    /*) ;;
    *) fail "path is not absolute: $path" ;;
  esac
  rest=${path#/}
  current=/
  old_ifs=$IFS
  IFS=/
  set -- $rest
  IFS=$old_ifs
  for component in "$@"; do
    [ -n "$component" ] || continue
    current=$current$component
    if [ -e "$current" ] || [ -L "$current" ]; then
      [ -d "$current" ] && [ ! -L "$current" ] || fail "unsafe path component: $current"
    fi
    current=$current/
  done
}

for name in \
  BDUI_DEPLOY_PROTOCOL_VERSION \
  BDUI_DEPLOY_SOURCE_REPO \
  BDUI_DEPLOY_TARGET_REMOTE \
  BDUI_DEPLOY_TARGET_BASE \
  BDUI_DEPLOY_MERGED_FLOOR_SHA \
  BDUI_DEPLOY_CANDIDATE_SHA \
  BDUI_DEPLOY_RELEASE_PATH \
  BDUI_DEPLOY_RECEIPT_PATH \
  BDUI_DEPLOY_ATTEMPT_ID; do
  need "$name"
done

[ "$BDUI_DEPLOY_PROTOCOL_VERSION" = "1" ] || fail "unsupported protocol version"
is_sha "$BDUI_DEPLOY_MERGED_FLOOR_SHA" || fail "invalid merged floor sha"
is_sha "$BDUI_DEPLOY_CANDIDATE_SHA" || fail "invalid candidate sha"
[ -n "${HOME-}" ] || fail "missing HOME"

PYTHON=$(command -v python3) || fail "missing python3"
command -v git >/dev/null 2>&1 || fail "missing git"
command -v make >/dev/null 2>&1 || fail "missing make"

case "$BDUI_DEPLOY_SOURCE_REPO" in
  /*) ;;
  *) fail "source repo is not absolute" ;;
esac
case "$BDUI_DEPLOY_SOURCE_REPO" in
  */|*/./*|*/../*) fail "source repo is not canonical" ;;
esac
[ -d "$BDUI_DEPLOY_SOURCE_REPO" ] || fail "source repo is not a directory"

data_home=${XDG_DATA_HOME:-$HOME/.local/share}
state_home=${XDG_STATE_HOME:-$HOME/.local/state}
case "$data_home" in /*) ;; *) fail "XDG_DATA_HOME is not absolute" ;; esac
case "$state_home" in /*) ;; *) fail "XDG_STATE_HOME is not absolute" ;; esac

source_base=$(basename "$BDUI_DEPLOY_SOURCE_REPO")
source_name=$(safe_component "$source_base" ws | cut -c1-40)
source_hash=$(sha256_text "$BDUI_DEPLOY_SOURCE_REPO" | cut -c1-12)
workspace_slug=$source_name-$source_hash
candidate_lower=$(printf '%s' "$BDUI_DEPLOY_CANDIDATE_SHA" | tr 'A-F' 'a-f')
release=$data_home/bdui/deploy/$workspace_slug/releases/$candidate_lower
attempt_safe=$(safe_component "$BDUI_DEPLOY_ATTEMPT_ID" attempt)
receipt=$state_home/bdui/$workspace_slug/deploy-receipts/$attempt_safe.json

[ "$BDUI_DEPLOY_RELEASE_PATH" = "$release" ] || fail "release path does not match protocol"
[ "$BDUI_DEPLOY_RECEIPT_PATH" = "$receipt" ] || fail "receipt path does not match protocol"
[ "$PWD" = "$release" ] || fail "adapter must run from the exact release cwd"
[ -d "$release" ] && [ ! -L "$release" ] || fail "release is not a directory"
assert_safe_existing_dirs "$release"
release_real=$(CDPATH= cd -P -- "$release" && pwd)
[ "$release_real" = "$release" ] || fail "release path resolves unexpectedly"
script_dir=$(CDPATH= cd -P -- "$(dirname -- "$0")" && pwd)
[ "$script_dir" = "$release/scripts" ] || fail "adapter is not candidate-local"

receipt_parent=$(dirname "$receipt")
assert_safe_existing_dirs "$receipt_parent"
if [ -e "$receipt" ] || [ -L "$receipt" ]; then
  fail "receipt already exists"
fi

head_before=$(git rev-parse HEAD) || fail "cannot read release HEAD"
[ "$head_before" = "$BDUI_DEPLOY_CANDIDATE_SHA" ] || fail "candidate HEAD mismatch"
status_before=$(git status --porcelain) || fail "cannot read release status"
[ -z "$status_before" ] || fail "release is dirty"
remote_before=$(git remote get-url origin) || fail "cannot read release origin"
[ "$remote_before" = "$BDUI_DEPLOY_TARGET_REMOTE" ] || fail "release origin mismatch"
git merge-base --is-ancestor "$BDUI_DEPLOY_MERGED_FLOOR_SHA" "$BDUI_DEPLOY_CANDIDATE_SHA" || fail "merged floor is not an ancestor"

make install-force

release_binary=$release/bd
installed_binary=$HOME/.local/bin/bd
alias_path=$HOME/.local/bin/beads
for binary in "$release_binary" "$installed_binary"; do
  [ -f "$binary" ] && [ ! -L "$binary" ] && [ -x "$binary" ] || fail "binary readback failed: $binary"
done
release_hash=$(sha256_file "$release_binary")
installed_hash=$(sha256_file "$installed_binary")
[ "$release_hash" = "$installed_hash" ] || fail "installed binary hash mismatch"
version_json=$("$installed_binary" version --json) || fail "installed version readback failed"
version_build=$(printf '%s' "$version_json" | "$PYTHON" -c '
import json
import sys
value = json.load(sys.stdin)
if not isinstance(value, dict) or not isinstance(value.get("build"), str):
    raise SystemExit("version JSON lacks string build")
print(value["build"])
') || fail "installed version JSON is invalid"
[ "$version_build" = "$(git rev-parse --short HEAD)" ] || fail "installed version build mismatch"
[ -L "$alias_path" ] || fail "alias is not a symlink"
alias_target=$(readlink "$alias_path") || fail "cannot read alias"
[ "$alias_target" = "bd" ] || fail "alias target mismatch"

head_after=$(git rev-parse HEAD) || fail "cannot reread release HEAD"
status_after=$(git status --porcelain) || fail "cannot reread release status"
remote_after=$(git remote get-url origin) || fail "cannot reread release origin"
[ "$head_after" = "$head_before" ] && [ "$status_after" = "$status_before" ] && [ "$remote_after" = "$remote_before" ] || fail "release changed during install"

export BDUI_RECEIPT_PATH=$receipt
export BDUI_RECEIPT_PARENT=$receipt_parent
export BDUI_RECEIPT_REPO=$BDUI_DEPLOY_SOURCE_REPO
export BDUI_RECEIPT_REMOTE=$BDUI_DEPLOY_TARGET_REMOTE
export BDUI_RECEIPT_BASE=$BDUI_DEPLOY_TARGET_BASE
export BDUI_RECEIPT_ATTEMPT=$BDUI_DEPLOY_ATTEMPT_ID
export BDUI_RECEIPT_FLOOR=$BDUI_DEPLOY_MERGED_FLOOR_SHA
export BDUI_RECEIPT_CANDIDATE=$BDUI_DEPLOY_CANDIDATE_SHA
export BDUI_RECEIPT_RELEASE=$release
export BDUI_RECEIPT_BINARY=$installed_binary
export BDUI_RECEIPT_BINARY_HASH=$installed_hash
export BDUI_RECEIPT_VERSION=$version_build
export BDUI_RECEIPT_ALIAS=$alias_path
export BDUI_RECEIPT_ALIAS_TARGET=$alias_target

"$PYTHON" - <<'PY'
import datetime
import hashlib
import json
import os
import tempfile

receipt_path = os.environ["BDUI_RECEIPT_PATH"]
parent = os.environ["BDUI_RECEIPT_PARENT"]
if os.path.lexists(receipt_path):
    raise SystemExit("receipt already exists")
os.makedirs(parent, mode=0o700, exist_ok=True)
if os.path.lexists(receipt_path):
    raise SystemExit("receipt already exists")
actions = [
    {"action": "build_install", "outcome": "success"},
    {"action": "binary_hash_readback", "outcome": "success"},
    {"action": "version_readback", "outcome": "success"},
    {"action": "alias_readback", "outcome": "success"},
]
digest = hashlib.sha256(json.dumps(actions, separators=(",", ":"), ensure_ascii=False).encode()).hexdigest()
candidate = os.environ["BDUI_RECEIPT_CANDIDATE"]
release = os.environ["BDUI_RECEIPT_RELEASE"]
receipt = {
    "protocol_version": 1,
    "repo": os.environ["BDUI_RECEIPT_REPO"],
    "target_remote": os.environ["BDUI_RECEIPT_REMOTE"],
    "target_base": os.environ["BDUI_RECEIPT_BASE"],
    "attempt_id": os.environ["BDUI_RECEIPT_ATTEMPT"],
    "merged_floor_sha": os.environ["BDUI_RECEIPT_FLOOR"],
    "candidate_sha": candidate,
    "verify": {"candidate_sha": candidate, "outcome": "success"},
    "previous_marker": None,
    "deployed_marker": candidate,
    "action_plan_digest": digest,
    "action_outcomes": actions,
    "deployment_source": {"path": release, "head_sha": candidate},
    "readback": {
        "outcome": "success",
        "deployed_marker": candidate,
        "source_path": release,
        "source_head": candidate,
        "installed_binary_path": os.environ["BDUI_RECEIPT_BINARY"],
        "installed_binary_sha256": os.environ["BDUI_RECEIPT_BINARY_HASH"],
        "installed_binary_build": os.environ["BDUI_RECEIPT_VERSION"],
        "alias_path": os.environ["BDUI_RECEIPT_ALIAS"],
        "alias_target": os.environ["BDUI_RECEIPT_ALIAS_TARGET"],
    },
    "outcome": "success",
    "completed_at": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z"),
}
payload = json.dumps(receipt, separators=(",", ":"), ensure_ascii=False).encode()
fd, temporary = tempfile.mkstemp(prefix=f".{os.path.basename(receipt_path)}.", dir=parent)
try:
    with os.fdopen(fd, "wb") as output:
        output.write(payload)
        output.flush()
        os.fsync(output.fileno())
    if os.path.lexists(receipt_path):
        raise FileExistsError(receipt_path)
    os.rename(temporary, receipt_path)
    temporary = None
    directory = os.open(parent, os.O_RDONLY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)
finally:
    if temporary is not None:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
PY
