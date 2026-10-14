#!/usr/bin/env bash
#
# Download and verify the proxy core for one target.
#
# Contract (ADR-0003, design baseline 10.2):
#   - the core is NEVER compiled here; only official prebuilt release binaries
#     are downloaded;
#   - every asset must be pinned by version AND sha256 in versions.lock.json;
#   - an asset is verified BEFORE it is placed where anything can use it;
#   - if a pin is missing, this script refuses. It must not download "whatever
#     is latest", and it must not skip verification.
#
# The refusal matters: a package that silently contains an unverified core
# looks complete to the user while carrying none of the integrity guarantees.
#
# Usage:
#   bash fetch-cores.sh --target-id windows-amd64 --out-dir stage/windows-amd64/cores
#
# NOTE: this file is intentionally ASCII-only (see environment-constraints.md).

set -euo pipefail

TARGET_ID=""
OUT_DIR=""
VERSION_DIR=""

while [ $# -gt 0 ]; do
    case "$1" in
        --target-id) TARGET_ID="$2"; shift 2 ;;
        --out-dir)   OUT_DIR="$2";   shift 2 ;;
        --version-dir) VERSION_DIR="$2"; shift 2 ;;
        -h|--help)
            sed -n '2,20p' "$0"
            exit 0 ;;
        *)
            echo "unknown argument: $1" >&2
            exit 2 ;;
    esac
done

if [ -z "$TARGET_ID" ] || [ -z "$OUT_DIR" ]; then
    echo "usage: $0 --target-id <id> --out-dir <dir> [--version-dir <dir>]" >&2
    exit 2
fi

# Default: resolve relative to this script (deploy/packaging -> deploy/../..).
if [ -z "$VERSION_DIR" ]; then
    script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
    VERSION_DIR=$(cd "${script_dir}/../.." && pwd)
fi

LOCK="${VERSION_DIR}/deploy/targets/versions.lock.json"
TARGETS="${VERSION_DIR}/deploy/targets/targets.json"

for f in "$LOCK" "$TARGETS"; do
    if [ ! -f "$f" ]; then
        echo "required file not found: $f" >&2
        exit 1
    fi
done

# --- 1) what does this target need? -----------------------------------------
# The target manifest is the single source of truth for asset architecture.
singbox_arch=$(jq -r --arg id "$TARGET_ID" '.targets[] | select(.id == $id) | .singBoxAssetArch // empty' "$TARGETS")

if [ -z "$singbox_arch" ]; then
    echo "target '$TARGET_ID' is not in $TARGETS, or its asset architecture is missing" >&2
    exit 1
fi

# --- 2) verify the pins BEFORE downloading anything -------------------------
# Check the pin up front so a missing pin fails fast, rather than after
# downloading a few hundred megabytes.
#
# Only sing-box is shipped (ADR-0005), so this is called once. It stays a
# function because the refusal must remain a per-asset check: the day a second
# asset is added, forgetting it here would be a silent hole.
check_pin() {
    local name="$1"
    local version sha
    version=$(jq -r --arg n "$name" ".sources[$n].version // empty" "$LOCK")
    sha=$(jq -r --arg n "$name" ".sources[$n].sha256 // empty" "$LOCK")
    if [ -z "$version" ] || [ "$version" = "null" ] || [ -z "$sha" ] || [ "$sha" = "null" ]; then
        return 1
    fi
    return 0
}

unresolved=""
check_pin sing_box_core || unresolved="${unresolved} sing_box_core"

if [ -n "$unresolved" ]; then
    echo "REFUSING to fetch cores: these pins are unresolved in versions.lock.json:${unresolved}" >&2
    echo "" >&2
    echo "A package must not bundle an unverified core. To resolve this:" >&2
    echo "  1. pick the exact release versions deliberately;" >&2
    echo "  2. record each asset filename and its sha256 in versions.lock.json;" >&2
    echo "  3. re-run the build." >&2
    echo "See design/adr/ADR-0003 and design/plan/STATUS.md." >&2
    exit 1
fi

echo "core pins are resolved; fetching assets for target ${TARGET_ID}"
echo "  sing-box arch: ${singbox_arch}"

# The actual download and extraction lives in the per-asset step below. It is
# intentionally not implemented as a best-effort path: until versions.lock.json
# carries real asset URLs and checksums, there is nothing correct to download.
#
# When the pins are filled in, each asset record must provide:
#   version, sha256, and one url per asset architecture.
# The fetch must then:
#   1. download to a temporary file,
#   2. compute sha256 and compare with the pin (mismatch => abort, delete),
#   3. only then move the binary into $OUT_DIR.
echo "::error::the download step is not implemented yet: versions.lock.json carries no asset URLs."
echo "This is expected until the core versions are pinned. The build must fail rather than"
echo "produce a package that claims to bundle cores." >&2
exit 1
