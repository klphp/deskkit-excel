# DeskKit Excel

An AI-driven batch processor for Excel files, running entirely on your own machine.

Describe what you want in one sentence, point it at a folder, and it works through
the files locally. Nothing is uploaded, and no account is required.

```
You:   merge the 200 department reports into one sheet, summarised by department
It:    reads 200 xlsx files → aligns headers → merges → groups → writes summary.xlsx
       all local, no upload, no account
```

## What it is actually for

The cases where the files are many, the formats are dirty, and the data is not
allowed to leave the machine:

- **Month-end report merging** — 200 department files with inconsistent headers.
- **Cleaning exported data** — blank rows, duplicates, broken dates, numbers stored as text.
  It shows you what it is about to change before changing anything.
- **Consolidating years of history** — pull selected columns out of many differently
  shaped workbooks into one clean table.

A cloud AI can't do these well: it can't read 3,000 files on your disk, and most
companies won't upload their financial workbooks to somebody else's server.

## Status

Early. This is version 0.1.0 and it is honest about that:

- **Excel only.** The tool registry is built for adding more file types (PDF, media),
  but nothing else is implemented yet.
- **Windows 10 and later.** The app refuses to start on older Windows. macOS builds
  exist and run, but are not polished.
- **Unsigned binaries.** SmartScreen and Gatekeeper will warn about an unknown
  developer. That is what unsigned means, not a sign of a problem.
- **AI output can be wrong.** The app lists pending changes and asks for
  confirmation before writing, but verify results yourself — especially amounts,
  IDs and dates.
- **No support promise.** Issues and suggestions are welcome; no response time is promised.

## Using AI

All batch operations work without AI. With a model connected you can also just
describe the job in words.

| Option | Notes |
|---|---|
| Your own key | Any OpenAI-compatible endpoint (DeepSeek, OpenAI, OpenRouter, …) |
| Local model | Auto-detects Ollama / LM Studio on this machine — keeps everything offline |

Keys are stored on the local machine only (`0600`), never uploaded.

## Build

Requirements: Go 1.26+, Node 22+, and the Wails v3 CLI. The session database uses
SQLCipher, so `CGO_ENABLED=1` is required.

```bash
wails3 dev                  # hot reload
wails3 build                # build
go test ./internal/...      # tests
./build-app.sh              # macOS .app bundle
```

On Windows with MSYS2, `gcc` from `ucrt64` must be on `PATH`, and the binary should be
linked with `-H windowsgui`, otherwise a console window opens alongside the app.

## Layout

```
main.go               entry: embeds config and frontend, calls boot.Run
internal/
  boot/               wiring and startup: config → local service → AI → single instance → window
  ai/                 model access: protocol, streaming, tool-call loop, error normalisation
  config/             config parsing (app.yaml / windows.yaml)
  server/             embedded local HTTP service
  window/             config-driven multi-window
  update/             self update
  tools/              capability packages (excel, fs)
config/app.yaml       app config (AI, service, update, i18n)
docs/                 design and integration notes
```

## Stack

Wails v3.1.0 · Go 1.26 · Vue 3 · TypeScript · Vite · SQLCipher (session storage).
File operations live in a separate library, [`fsx`](https://github.com/klphp/fsx).

## License

MIT — see [LICENSE](./LICENSE).

---

## 中文

**AI 驱动的 Excel 批量处理工具，全部在本机完成。**

一句话下指令，指向一个文件夹，它在本地把文件处理完。不上传、不需要账号。

适合的场景都是「文件多、格式脏、数据不能外传」：月末合并几百个部门报表、
清洗导出的脏数据、把多年格式不一的历史表合成一张。云端 AI 做不了这些——
它读不到你硬盘上的几千个文件，企业也不允许把财务表传到别人的服务器。

现状（0.1.0）：

- 只做了 Excel；后续文件类型可以接进同一套注册表，但尚未实现。
- Windows 10 及以上；更老的系统会直接退出。macOS 能跑，但没做打磨。
- 二进制未签名，SmartScreen / Gatekeeper 会提示「未知开发者」。
- AI 输出可能出错：写文件前会列出将要改的内容让你确认，金额、编号、日期请自行核对。
- 不承诺技术支持时间。

不接 AI 也能用全部批处理功能；接上之后可以用人话下指令，密钥只存本机（`0600`）。
