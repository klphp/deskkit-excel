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
  echo "✗ 前端出现 new ...Service()：Wails 绑定是模块命名空间，不是构造函数" >&2
  echo "$hits" | sed 's/^/  /' >&2
  echo "  正确写法：import * as Services from '.../internal/services'; Services.XxxService.Method()" >&2
  fail=1
fi

# ② 绑定必须全是 TypeScript（生成时带 -ts），混入 .js 说明用了默认参数
js_count=$(find frontend/bindings -name '*.js' 2>/dev/null | wc -l | tr -d ' ')
if [ "$js_count" -gt 0 ]; then
  echo "✗ frontend/bindings 里有 $js_count 个 .js 文件：绑定是用默认参数生成的" >&2
  echo "  重跑 ./scripts/gen-bindings.sh（固定 -ts -i）" >&2
  find frontend/bindings -name '*.js' | head -3 | sed 's/^/  /' >&2
  fail=1
fi

# ③ 静默 catch 吞掉绑定错误：允许空 catch，但不允许注释里写着 bindings 却什么都不做
if hits=$(grep -rnE "catch *\{ *(/\* *bindings" frontend/src 2>/dev/null); then
  echo "✗ 有静默吞掉绑定错误的 catch：出问题时界面会显示错误的结论" >&2
  echo "$hits" | sed 's/^/  /' >&2
  echo "  改成 console.warn(e)，让问题可见" >&2
  fail=1
fi

if [ "$fail" -eq 0 ]; then
  echo "  ✓ 前端守卫通过（无 new Service、绑定纯 TS、无静默 catch）"
fi
exit "$fail"
