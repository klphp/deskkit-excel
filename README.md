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

- **Excel only.** That is the whole scope. One tool, one job.
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
```

## Stack

Wails v3.1.0 · Go 1.26 · Vue 3 · TypeScript · Vite · SQLCipher (session storage).
File operations live in a separate library, [`fsx`](https://github.com/klphp/fsx).

## License

MIT — see [LICENSE](./LICENSE).
