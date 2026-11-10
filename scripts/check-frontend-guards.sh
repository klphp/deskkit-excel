#!/usr/bin/env bash
# 前端守卫：把两类"能编译但运行时必炸/必误导"的写法挡在构建前。
#
# 为什么需要：这两个问题都不会让 TypeScript 报错，只在用户点按钮时才暴露，
# 而且表现为误导性提示（例如"没配置更新源"），排查时要翻后端日志才能发现。
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0

# ① 绑定是模块命名空间，不能用 new 实例化
# 注释里出现 "new XxxService()" 是解释性文字，不算违规：先剔掉以 * // /* 开头的行
hits=$(grep -rnE "new +[A-Za-z0-9_]*Service\(" frontend/src --include=*.vue --include=*.ts 2>/dev/null \
  | grep -vE ":[[:space:]]*(\*|//|/\*)" || true)
if [ -n "$hits" ]; then
  echo "FAIL: frontend uses new ...Service() - Wails bindings are module namespaces, not constructors" >&2
  echo "$hits" | sed 's/^/  /' >&2
  echo "  do this instead: import * as Services from '.../internal/services'; Services.XxxService.Method()" >&2
  fail=1
fi

# ② 绑定必须全是 TypeScript（生成时带 -ts），混入 .js 说明用了默认参数
js_count=$(find frontend/bindings -name '*.js' 2>/dev/null | wc -l | tr -d ' ')
if [ "$js_count" -gt 0 ]; then
  echo "FAIL: frontend/bindings holds $js_count .js file(s) - bindings were generated with default flags" >&2
  echo "  rerun ./scripts/gen-bindings.sh (it pins -ts -i)" >&2
  find frontend/bindings -name '*.js' | head -3 | sed 's/^/  /' >&2
  fail=1
fi

# ③ 静默 catch 吞掉绑定错误：允许空 catch，但不允许注释里写着 bindings 却什么都不做
if hits=$(grep -rnE "catch *\{ *(/\* *bindings" frontend/src 2>/dev/null); then
  echo "FAIL: a catch block swallows binding errors - the UI would then show a wrong conclusion" >&2
  echo "$hits" | sed 's/^/  /' >&2
  echo "  use console.warn(e) so the problem is visible" >&2
  fail=1
fi

if [ "$fail" -eq 0 ]; then
  echo "  frontend guards passed (no new Service, bindings are plain TS, no silent catch)"
fi
exit "$fail"
