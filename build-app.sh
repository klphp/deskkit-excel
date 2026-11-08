#!/usr/bin/env bash
#
# 打一个可以双击运行的 macOS 应用包。
#
# 为什么需要这个脚本：`wails3 build` 只产出 bin/deskkit 这个裸二进制。
# 裸二进制双击没反应（Finder 不知道怎么跑它），用户拿到手不知道从哪开始。
# 这个脚本把它装进标准的 .app 结构 + Info.plist，并用 ad-hoc 签名，
# 让它在当前机器上能直接打开。
#
# 分发给别人之前还差一步：正式签名的 Developer ID 证书和公证，
# 否则 Gatekeeper 会拦（未签名的应用在 macOS 15 上已经没有「右键打开」的绕法）。
set -euo pipefail

# ★ cgo 必须开：会话库用 SQLCipher（C 库），关掉 cgo 会链接失败。
export CGO_ENABLED=1

APP_NAME="DeskKit Excel"
VERSION="${VERSION:-0.1.0}"
BIN="bin/deskkit"
OUT="dist/${APP_NAME}.app"

if [[ ! -f "$BIN" ]]; then
  echo "先跑 wails3 build 生成 $BIN" >&2
  exit 1
fi

rm -rf "$OUT"
mkdir -p "$OUT/Contents/MacOS" "$OUT/Contents/Resources"

cp "$BIN" "$OUT/Contents/MacOS/$APP_NAME"
chmod +x "$OUT/Contents/MacOS/$APP_NAME"

# 图标可选：没有也能跑，只是 Dock 里是默认图标
if [[ -f build/appicon.png ]]; then
  cp build/appicon.png "$OUT/Contents/Resources/appicon.png"
fi

cat > "$OUT/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>${APP_NAME}</string>
  <key>CFBundleDisplayName</key><string>${APP_NAME}</string>
  <key>CFBundleExecutable</key><string>${APP_NAME}</string>
  <key>CFBundleIdentifier</key><string>com.klphp.deskkit</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${VERSION}</string>
  <key>CFBundleVersion</key><string>${VERSION}</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSRequiresAquaSystemAppearance</key><false/>
</dict>
</plist>
PLIST

# ad-hoc 签名：让系统认为这个包是完整的（不解决 Gatekeeper 的「未识别开发者」）
codesign --force --deep --sign - "$OUT" >/dev/null 2>&1 || true

echo "已生成：$OUT"
echo "打开：open \"$OUT\""
