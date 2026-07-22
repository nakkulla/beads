#!/bin/bash
# Check that all version files are in sync
# Run this before committing version bumps

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

# Get the canonical version from version.go
CANONICAL=$(grep 'Version = ' cmd/bd/version.go | sed 's/.*"\(.*\)".*/\1/')

if [ -z "$CANONICAL" ]; then
    echo -e "${RED}❌ Could not read version from cmd/bd/version.go${NC}"
    exit 1
fi

# The canonical version may carry a fork-identifying pre-release suffix
# (e.g. "1.1.0-fork.1"). The downstream version surfaces below intentionally
# track the upstream base version ("1.1.0"), so compare them against the base
# version with any "-fork.<N>" suffix stripped. For a plain canonical version
# BASE_VERSION equals CANONICAL, preserving the original behavior.
BASE_VERSION=$(printf '%s' "$CANONICAL" | sed -E 's/-fork\.[0-9]+$//')

echo "Canonical version (from version.go): $CANONICAL"
if [ "$BASE_VERSION" != "$CANONICAL" ]; then
    echo "Base version (surfaces compared against): $BASE_VERSION"
fi
echo ""

MISMATCH=0

check_version() {
    local _file=$1
    local version=$2
    local description=$3

    if [ "$version" != "$BASE_VERSION" ]; then
        echo -e "${RED}❌ $description: $version (expected $BASE_VERSION)${NC}"
        MISMATCH=1
    else
        echo -e "${GREEN}✓ $description: $version${NC}"
    fi
}

# Check all version files
check_version "integrations/beads-mcp/pyproject.toml" \
    "$(grep '^version = ' integrations/beads-mcp/pyproject.toml 2>/dev/null | sed 's/.*"\(.*\)".*/\1/')" \
    "MCP pyproject.toml"

check_version "integrations/beads-mcp/src/beads_mcp/__init__.py" \
    "$(grep '__version__ = ' integrations/beads-mcp/src/beads_mcp/__init__.py 2>/dev/null | sed 's/.*"\(.*\)".*/\1/')" \
    "MCP __init__.py"

check_version "plugins/beads/.claude-plugin/plugin.json" \
    "$(jq -r '.version' plugins/beads/.claude-plugin/plugin.json 2>/dev/null)" \
    "Claude plugin.json"

check_version "plugins/beads/.codex-plugin/plugin.json" \
    "$(jq -r '.version' plugins/beads/.codex-plugin/plugin.json 2>/dev/null)" \
    "Codex plugin.json"

check_version ".claude-plugin/marketplace.json" \
    "$(jq -r '.plugins[0].version' .claude-plugin/marketplace.json 2>/dev/null)" \
    "Claude marketplace.json"

check_version "npm-package/package.json" \
    "$(jq -r '.version' npm-package/package.json 2>/dev/null)" \
    "npm package.json"

# Hook templates are now generated dynamically by cmd/bd/hooks.go using the
# Version constant from version.go, so no separate file check is needed.
# (Previously checked cmd/bd/templates/hooks/pre-commit which no longer exists.)

echo ""

if ! ./scripts/check-docs-version.sh; then
    MISMATCH=1
fi

echo ""

if [ $MISMATCH -eq 1 ]; then
    echo -e "${RED}❌ Version mismatch detected!${NC}"
    echo ""
    echo "Run: scripts/update-versions.sh $BASE_VERSION"
    echo "Or manually update the mismatched files."
    exit 1
else
    if [ "$BASE_VERSION" != "$CANONICAL" ]; then
        echo -e "${GREEN}✓ Version files and released-docs policy pass for: $CANONICAL (surfaces base $BASE_VERSION)${NC}"
    else
        echo -e "${GREEN}✓ Version files and released-docs policy pass for: $CANONICAL${NC}"
    fi
fi
