#!/usr/bin/env bash
# 生成 Wails 绑定 —— 参数固定，任何人任何机器跑出来都一样。
#
# 为什么必须用脚本而不是记在脑子里：
#   wails3 generate bindings              → .js + class    （默认）
#   wails3 generate bindings -ts -i       → .ts + interface（本项目采用）
# 少一个 -ts，整个 frontend/bindings 换一套形态，而前端调用代码是照抄的，
# 表现就是「后端日志说功能已启用，界面说没配置」这类假象。
#
# 用法：./scripts/gen-bindings.sh
set -euo pipefail

cd "$(dirname "$0")/.."

if ! command -v wails3 >/dev/null 2>&1; then
  echo "找不到 wails3。按 go.mod 里的版本安装：" >&2
  echo '  go install github.com/wailsapp/wails/v3/cmd/wails3@$(go list -m -f "{{.Version}}" github.com/wailsapp/wails/v3)' >&2
  exit 1
fi

WANT=$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v3)
HAVE=$(wails3 version 2>&1 | head -1 | grep -oE "v?[0-9]+\.[0-9]+\.[0-9]+" | head -1 | tr -d 'v')
if [ "$WANT" != "$HAVE" ]; then
  echo "注意：CLI 版本 $HAVE 与 go.mod 的 $WANT 不一致，绑定可能生成出不同形态。" >&2
  echo "建议改成一致：go install github.com/wailsapp/wails/v3/cmd/wails3@$WANT" >&2
fi

# 固定参数：-ts 生成 TypeScript，-i 生成 interface（不用 class）
wails3 generate bindings -ts -i -clean

echo "── 生成结果检查 ──"
count_ts=$(find frontend/bindings -name '*.ts' | wc -l | tr -d ' ')
count_js=$(find frontend/bindings -name '*.js' | wc -l | tr -d ' ')
echo "  .ts 文件 $count_ts 个，.js 文件 $count_js 个"
if [ "$count_ts" -eq 0 ]; then
  echo "✗ 没有生成 TypeScript 绑定，参数可能没生效" >&2
  exit 1
fi
if [ "$count_js" -gt 0 ]; then
  echo "✗ 出现了 .js 绑定，说明这次生成用的是默认参数（缺 -ts）" >&2
  exit 1
fi
echo "  ✓ 绑定形态正确（纯 TypeScript）"
