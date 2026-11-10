# DeskKit Excel

An AI-driven batch processor for Excel files that runs entirely on your machine.
Describe the job in one sentence, point it at a folder, and it works through the
files locally — no account, and the files never leave your computer.

## What's new in 0.1.1

- Windows builds now carry the application icon and DPI manifest (the file no longer
  shows up as a blank generic exe in Explorer).
- The updater is configured and points at this repository, so the in-app
  "check for updates" works from this release on.

## What it does

- **Merge** many workbooks or CSVs into one table, aligning headers by column name
- **Clean** exported data: blank rows, duplicates, broken dates, numbers stored as text
- **De-duplicate** by any key column
- **Split** one file into many by a column value, or merge many sheets into one
- **Extract / rename / compute** columns
- **Compare** two versions of a file to see exactly what changed
- **Convert** between xlsx and CSV, including GBK for older Chinese ERP and accounting software

Ask for any of these in plain language, or use the operations directly — AI is optional.

## Install

Windows 10 or later:

1. Download the `deskkit-<version>-windows-amd64.zip` from the assets below and extract it — there is no installer.
2. Run `deskkit.exe`. SmartScreen will say "unrecognised app" because the build is
   unsigned: choose **More info → Run anyway**.
3. Verify the download against `SHA256SUMS` if you want to be sure it arrived intact.

Older Windows versions are refused at startup on purpose.

## Requirements

- **A model is needed for the chat interface.** Any OpenAI-compatible endpoint
  (DeepSeek, OpenAI, OpenRouter) or a local model via Ollama / LM Studio.
  Without a model, the batch operations still work; you just can't talk to it.
- **Files stay local.** Processing happens on your machine. If you configure a cloud
  model, your messages and the results the tools return are sent to that provider —
  use a local model if that matters to you.

## Known limitations

- Excel only, by design. This is the whole scope.
- Windows is the supported platform. macOS builds exist but are not polished.
- Unsigned: Windows SmartScreen and macOS Gatekeeper will warn.
- A merge holds the result in memory, so a merge producing roughly 1 GB of data is
  refused with an explanation rather than risking your machine.
- AI output can be wrong. Every write shows what it is about to change first, but
  verify amounts, IDs and dates yourself.

## Verify

`SHA256SUMS` is attached to this release. Checksums protect against a corrupted
download; they are not a signature.

---

MIT licensed. Source, issues and discussion: https://github.com/klphp/deskkit-excel
