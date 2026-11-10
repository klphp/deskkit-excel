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
  echo "用法：$0 <tag> [--local]" >&2
  exit 2
fi
VER="${TAG#v}"

echo "── ① tag 与配置版本一致 ──"
CFG_VER=$(grep -E "^  version:" config/app.yaml | head -1 | awk '{print $2}' | tr -d '"')
if [ "$CFG_VER" = "$VER" ]; then
  ok "config version = $CFG_VER"
else
  bad "config version = $CFG_VER，但 tag = $TAG —— 客户端比较的是配置里的版本，不匹配就永远看不到更新"
fi

if [ "$MODE" = "--local" ]; then
  [ "$fail" -eq 0 ] && echo "本地一致性检查通过" || echo "本地一致性检查失败" >&2
  exit "$fail"
fi

echo "── ② Release 存在且非 draft / 非 prerelease ──"
REL_JSON=$(gh release view "$TAG" --repo "$REPO" --json tagName,isDraft,isPrerelease,assets 2>/dev/null)
if [ -z "$REL_JSON" ]; then
  bad "找不到 Release $TAG"
  exit "$fail"
fi
IS_DRAFT=$(echo "$REL_JSON" | python3 -c "import json,sys;print(json.load(sys.stdin)['isDraft'])")
IS_PRE=$(echo "$REL_JSON" | python3 -c "import json,sys;print(json.load(sys.stdin)['isPrerelease'])")
[ "$IS_DRAFT" = "False" ] && ok "不是 draft" || bad "是 draft：releases/latest 不会返回它"
[ "$IS_PRE" = "False" ] && ok "不是 prerelease" || bad "是 prerelease：releases/latest 不会返回它"

echo "── ③ 每个目标平台都有匹配的资产 ──"
ASSETS=$(echo "$REL_JSON" | python3 -c "import json,sys;print(' '.join(a['name'] for a in json.load(sys.stdin)['assets']))")
echo "  资产：$ASSETS"
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
  if [ -n "$hit" ]; then ok "$spec → $hit"; else bad "$spec 没有可用资产（文件名需同时含 $plat 与 $arch）"; fi
done

echo "── ④ SHA256SUMS 与真实文件一致 ──"
SUMS_URL="https://github.com/$REPO/releases/download/$TAG/SHA256SUMS"
TMP=$(mktemp -d)
if ! curl -sfL "$SUMS_URL" -o "$TMP/SHA256SUMS"; then
  bad "下载不到 SHA256SUMS（update.checksumAsset 指向它）"
else
  ok "SHA256SUMS 存在"
  for a in $ASSETS; do
    case "$a" in
      SHA256SUMS|*.sig|*.asc|*SHA256SUMS*.txt) continue ;;
    esac
    want=$(awk -v n="$a" '$2==n || $2=="*"n {print $1}' "$TMP/SHA256SUMS" | head -1)
    if [ -z "$want" ]; then
      bad "$a 不在 SHA256SUMS 里"
      continue
    fi
    curl -sfL "https://github.com/$REPO/releases/download/$TAG/$a" -o "$TMP/$a" || { bad "$a 下载失败"; continue; }
    got=$(shasum -a 256 "$TMP/$a" 2>/dev/null | awk '{print $1}' || sha256sum "$TMP/$a" | awk '{print $1}')
    if [ "$want" = "$got" ]; then ok "$a sha256 一致"; else bad "$a sha256 不一致（表里 $want，实际 $got）"; fi
  done
fi
rm -rf "$TMP"

echo
if [ "$fail" -eq 0 ]; then
  echo "全部通过：这个 Release 可以被客户端正确发现并安装。"
else
  echo "有项目未通过 —— 不要对外宣称这版可用。" >&2
fi
exit "$fail"
