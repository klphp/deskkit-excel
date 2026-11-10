#!/usr/bin/env bash
# 一轮验证几秒钟：起本地模拟更新源 → 跑自检 → 看结果。全程不碰 GitHub。
#
#   ./scripts/update-sim.sh                 # 默认模拟 9.9.9
#   ./scripts/update-sim.sh 0.1.4           # 模拟指定版本
#
# 原理：tools/mock-updater 伪装成 api.github.com 的返回结构，
# 客户端配置里 update.baseURL 指到它 —— 走的是和生产完全同一条代码路径，
# 只有地址变了。所以本地跑通 = 线上大概率也通。
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-9.9.9}"
PORT="${PORT:-8790}"
SIMDIR="${SIMDIR:-/tmp/deskkit-sim}"
DEVCFG="${DEVCFG:-/tmp/dev-app.yaml}"

mkdir -p "$SIMDIR"

echo "── ① 起模拟更新源（版本 $VERSION，端口 $PORT）──"
go run ./tools/mock-updater --version "$VERSION" --port "$PORT" --dir "$SIMDIR" >/tmp/mock-updater.log 2>&1 &
MOCK_PID=$!
# 退出时收好自己的进程，别留给下一轮占端口
trap 'kill "$MOCK_PID" 2>/dev/null || true' EXIT
for _ in $(seq 1 40); do
  curl -sf "http://127.0.0.1:$PORT/" >/dev/null 2>&1 && break
  sleep 0.25
done
if ! curl -sf "http://127.0.0.1:$PORT/" >/dev/null 2>&1; then
  echo "模拟服务没起来，日志：" >&2
  tail -20 /tmp/mock-updater.log >&2
  exit 1
fi

echo "── ② 生成指向本地的开发配置 ──"
python3 - "$PORT" "$DEVCFG" <<'PY'
import pathlib, sys
port, out = sys.argv[1], sys.argv[2]
src = pathlib.Path("config/app.yaml").read_text(encoding="utf-8")
dev = src.replace('  repository: "klphp/deskkit-excel"',
                  f'  repository: "klphp/deskkit-excel"\n  baseURL: "http://127.0.0.1:{port}"', 1)
pathlib.Path(out).write_text(dev, encoding="utf-8")
print(f"  {out}")
PY

echo "── ③ 自检（不需要开窗口、不需要发版）──"
if ! CGO_ENABLED=1 go build -tags production -o bin/deskkit . ; then
  echo "构建失败" >&2
  exit 1
fi
DESKKIT_CONFIG="$DEVCFG" ./bin/deskkit --check-update | tee /tmp/update-probe.json

echo
echo "判读：available=true 且有 asset、checksum=ok → 整条链路正常。"
echo "想验证界面上那句话，再跑：./bin/deskkit（配置已经指向本地源）"
