#!/usr/bin/env bash
# 发版后复核：这一个脚本就是"更新功能不许出错"的闸门。
#
# 用法：
#   ./scripts/verify-release.sh v0.1.3          # 校验已有 Release
#   ./scripts/verify-release.sh v0.1.3 --local  # 只校验本地 tag 与配置一致性
#
# 校验项（任一不符即退出码 1）：
#   1. tag 去掉 v 之后，必须等于 config/app.yaml 里的 app.version
#      —— 不等的话客户端永远看不到更新（它比较的是配置里的版本）
#   2. Release 必须存在，且不是 draft / prerelease
#      —— releases/latest 不返回 draft 与 prerelease
#   3. 每个目标平台都必须有资产，且文件名同时含平台名与架构名
#      —— Wails 的 DefaultAssetMatcher 靠这个匹配，写成 win-x64 就取不到
#   4. SHA256SUMS 必须存在，且与每个资产的真实 sha256 一致
#      —— 不一致客户端会拒绝安装
set -uo pipefail
cd "$(dirname "$0")/.."

TAG="${1:-}"
MODE="${2:-}"
REPO="${REPO:-klphp/deskkit-excel}"
# 目标平台：与 CI matrix 对应
PLATFORMS="${PLATFORMS:-windows/amd64 darwin/amd64 darwin/arm64}"

fail=0
ok()   { echo "  ✓ $*"; }
bad()  { echo "  ✗ $*" >&2; fail=1; }

if [ -z "$TAG" ]; then
  echo "usage: $0 <tag> [--local]" >&2
  exit 2
fi
VER="${TAG#v}"

echo "-- 1) tag matches the version in config --"
CFG_VER=$(grep -E "^  version:" config/app.yaml | head -1 | awk '{print $2}' | tr -d '"')
if [ "$CFG_VER" = "$VER" ]; then
  ok "config version = $CFG_VER"
else
  bad "config version = $CFG_VER but tag = $TAG - clients compare the version in config, so a mismatch means updates are never offered"
fi

if [ "$MODE" = "--local" ]; then
  [ "$fail" -eq 0 ] && echo "local consistency check passed" || echo "local consistency check failed" >&2
  exit "$fail"
fi

echo "-- 2) release exists and is neither draft nor prerelease --"
REL_JSON=$(gh release view "$TAG" --repo "$REPO" --json tagName,isDraft,isPrerelease,assets 2>/dev/null)
if [ -z "$REL_JSON" ]; then
  bad "release $TAG not found"
  exit "$fail"
fi
IS_DRAFT=$(echo "$REL_JSON" | python3 -c "import json,sys;print(json.load(sys.stdin)['isDraft'])")
IS_PRE=$(echo "$REL_JSON" | python3 -c "import json,sys;print(json.load(sys.stdin)['isPrerelease'])")
[ "$IS_DRAFT" = "False" ] && ok "not a draft" || bad "it is a draft: releases/latest never returns drafts"
[ "$IS_PRE" = "False" ] && ok "not a prerelease" || bad "it is a prerelease: releases/latest never returns prereleases"

echo "-- 3) every target platform has a matching asset --"
ASSETS=$(echo "$REL_JSON" | python3 -c "import json,sys;print(' '.join(a['name'] for a in json.load(sys.stdin)['assets']))")
echo "  assets: $ASSETS"
for spec in $PLATFORMS; do
  plat="${spec%/*}"; arch="${spec#*/}"
  hit=$(for a in $ASSETS; do
    low=$(echo "$a" | tr '[:upper:]' '[:lower:]')
    case "$low" in
      *.sig|*.asc) continue ;;
      *sha256sums*) continue ;;
    esac
    echo "$low" | grep -q "$plat" && echo "$low" | grep -q "$arch" && echo "$a"
  done | head -1)
  if [ -n "$hit" ]; then ok "$spec → $hit"; else bad "$spec has no usable asset (file name must contain both $plat and $arch)"; fi
done

echo "-- 3b) macOS must ship a DMG (for manual install) --"
for spec in $PLATFORMS; do
  plat="${spec%/*}"; arch="${spec#*/}"
  [ "$plat" = "darwin" ] || continue
  hit=$(for a in $ASSETS; do
    low=$(echo "$a" | tr '[:upper:]' '[:lower:]')
    case "$low" in *.dmg) echo "$low" | grep -q "darwin" && echo "$low" | grep -q "$arch" && echo "$a" ;; esac
  done | head -1)
  if [ -n "$hit" ]; then ok "$spec dmg → $hit"; else bad "$spec has no .dmg (Mac users would have nothing to drag into Applications)"; fi
done

# Windows 的 zip 必须只有一个顶层条目（就是 exe 本身）。
# Wails 更新器解压后要求「只有一个顶层条目」并把它顶替运行中的 exe；
# 包里多一个 README 就会让用户点更新后什么都不发生（实测踩过）。
echo "-- 3c) the Windows zip holds exactly one top-level file --"
for spec in $PLATFORMS; do
  plat="${spec%/*}"; arch="${spec#*/}"
  [ "$plat" = "windows" ] || continue
  win_asset=$(for a in $ASSETS; do
    low=$(echo "$a" | tr '[:upper:]' '[:lower:]')
    case "$low" in *.zip) echo "$low" | grep -q "windows" && echo "$low" | grep -q "$arch" && echo "$a" ;; esac
  done | head -1)
  [ -n "$win_asset" ] || continue
  ztmp=$(mktemp -d)
  if curl -sfL "https://github.com/$REPO/releases/download/$TAG/$win_asset" -o "$ztmp/a.zip"; then
    top=$(unzip -Z1 "$ztmp/a.zip" | awk -F/ 'NF==1 || $2=="" {print $1}' | sort -u)
    count=$(printf '%s\n' "$top" | grep -c . || true)
    if [ "$count" -eq 1 ] && printf '%s' "$top" | grep -qi '\.exe$'; then
      ok "$win_asset → single entry: $top"
    else
      bad "$win_asset has $count top-level entries ($(printf '%s' "$top" | tr '\n' ' ')) - the updater needs exactly one, and it must be the exe"
    fi
  else
    bad "could not download $win_asset"
  fi
  rm -rf "$ztmp"
done

echo "-- 4) SHA256SUMS matches the real files --"
SUMS_URL="https://github.com/$REPO/releases/download/$TAG/SHA256SUMS"
TMP=$(mktemp -d)
if ! curl -sfL "$SUMS_URL" -o "$TMP/SHA256SUMS"; then
  bad "cannot download SHA256SUMS (update.checksumAsset points at it)"
else
  ok "SHA256SUMS present"
  for a in $ASSETS; do
    case "$a" in
      SHA256SUMS|*.sig|*.asc|*SHA256SUMS*.txt) continue ;;
    esac
    # zip 与 dmg 都校验（dmg 是下载安装包，zip 是更新包）
    want=$(awk -v n="$a" '$2==n || $2=="*"n {print $1}' "$TMP/SHA256SUMS" | head -1)
    if [ -z "$want" ]; then
      bad "$a is missing from SHA256SUMS"
      continue
    fi
    curl -sfL "https://github.com/$REPO/releases/download/$TAG/$a" -o "$TMP/$a" || { bad "$a failed to download"; continue; }
    got=$(shasum -a 256 "$TMP/$a" 2>/dev/null | awk '{print $1}' || sha256sum "$TMP/$a" | awk '{print $1}')
    if [ "$want" = "$got" ]; then ok "$a sha256 matches"; else bad "$a sha256 mismatch (manifest says $want, actual $got)"; fi
  done
fi
rm -rf "$TMP"

echo
if [ "$fail" -eq 0 ]; then
  echo "all checks passed: clients can discover and install this release."
else
  echo "some checks failed - do not announce this release as usable." >&2
fi
exit "$fail"
