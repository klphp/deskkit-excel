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

# ★ 前端也要在这里构建，否则 dist 还是上一次的。
  ./scripts/check-frontend-guards.sh
# 前后端两条命令分开跑，最容易出现「改了前端、忘了 npm run build，然后
# 只跑打包」—— 界面纹丝不动，人却以为代码没生效。一条命令做完最稳。
if [[ "${SKIP_FRONTEND:-0}" != "1" ]]; then
  echo "构建前端..."
  ( cd frontend && npm run build ) || { echo "前端构建失败" >&2; exit 1; }
fi

# ★ 一定要在这里重新编译二进制，不能复用 bin/deskkit。
# 前端 dist 是用 go:embed 打进二进制的：只重跑 npm run build、再拿旧二进制打包，
# .app 里跑的还是上一版界面 —— 现象就是「代码改了、界面没变」，
# 而且极难查（会怀疑是缓存、是热更新、是 WebView 不刷新）。
echo "编译 Go（前端 dist 会被 embed 进去）..."
go build -tags production -trimpath -ldflags="-w -s" -o "$BIN" .

if [[ ! -f "$BIN" ]]; then
  echo "编译失败：没有产出 $BIN" >&2
  exit 1
fi

rm -rf "$OUT"
mkdir -p "$OUT/Contents/MacOS" "$OUT/Contents/Resources"

cp "$BIN" "$OUT/Contents/MacOS/$APP_NAME"
chmod +x "$OUT/Contents/MacOS/$APP_NAME"

# 图标。★ 只放 png 到 Resources 是没用的 —— macOS 靠 Info.plist 的
# CFBundleIconFile 去认，名字对不上就用系统默认图标（实测踩过：
# Dock 里一直是那个默认方块）。所以统一用 .icns + 写进 plist。
ICON_KEY=""
if [[ -f build/darwin/icons.icns ]]; then
  cp build/darwin/icons.icns "$OUT/Contents/Resources/appicon.icns"
  ICON_KEY='  <key>CFBundleIconFile</key><string>appicon.icns</string>
'
elif [[ -f build/appicon.png ]]; then
  # 兜底：没有 icns 时至少放 png（新版 macOS 能认，老版本不保证）
  cp build/appicon.png "$OUT/Contents/Resources/appicon.png"
  ICON_KEY='  <key>CFBundleIconFile</key><string>appicon.png</string>
'
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
${ICON_KEY}
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
