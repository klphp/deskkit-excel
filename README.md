# DeskKit

**本地优先的 AI 桌面工具集。数据一步都不出你的机器。**

云端 AI 拿不到你硬盘上的文件，企业也不允许把表格传到别人服务器。
DeskKit 把 AI 装进桌面应用里：**你说话，它在本地干活**。

```
一个内核  →  多个工具
           ├── Excel   批量合并 / 清洗 / 汇总          ← 开发中
           ├── PDF     合并 / 拆分 / 提取表格
           └── …       按需增加
```

## 为什么值得做「桌面」而不是网页

| | 云端 AI | DeskKit |
|---|---|---|
| 能不能读你本地的 3000 个文件 | ❌ 上传一个个来 | ✅ 直接扫目录 |
| 数据要不要出机器 | ❌ 要 | ✅ 不要 |
| 批量任务 | ❌ 一次一个 | ✅ 一次一批 |
| 需要账号吗 | ❌ 要 | ✅ 不要 |
| 断网能用吗 | ❌ 不能 | ✅ 能（配合本地模型） |

## 技术栈

```
Wails v3.1.0 + Go 1.26 + Vue 3 + TypeScript + Vite
AI：任意 OpenAI 兼容端点 + 自动探测本地 Ollama / LM Studio
单实例锁、自更新、内嵌 Gin 本地接口、配置驱动多窗口
```

## 目录结构

```
main.go               入口：内嵌配置与前端，调 boot.Run
internal/
  boot/               ★ 装配与启动（读配置 → 起服务 → 装 AI → 单实例 → 开窗口）
  ai/                 AI 接入层：协议、流式、工具调用循环、错误归一
  config/             配置解析（app.yaml / windows.yaml）
  server/             内嵌本地 HTTP 服务
  window/             配置驱动的多窗口管理
  update/             自更新
  services/           暴露给前端的门面
config/
  app.yaml            应用配置（AI、服务、更新、i18n）
  windows.yaml        窗口定义（加一段 = 多一个窗口）
docs/
  ai.md               AI 接入：密钥、流式、工具调用、错误分类
  single-instance.md  禁止多开与端口的关系
  auto-update.md      自更新
```

**每个工具的 `main.go` 只有二十来行**：内嵌自己的配置与前端，其余交给 `internal/boot`。
这样加第二个工具不需要重新发明启动流程。

## 开发

```bash
wails3 dev            # 热重载
wails3 build          # 构建
go test ./internal/... # 测试
```

## 文档

- [AI 接入](docs/ai.md) — 怎么接自己的模型、流式、工具调用
- [禁止多开](docs/single-instance.md) — 单实例锁，以及内嵌服务端口的注意事项
- [自更新](docs/auto-update.md) — 更新源、验签、踩过的坑

## 授权

闭源软件，保留所有权利。详见 `LICENSE.txt`。
