# DeskKit Excel

An AI-driven batch processor for Excel files that runs entirely on your machine.
Describe the job in one sentence, point it at a folder, and it works through the
files locally — no account, and the files never leave your computer.

## What's new in 0.2.0

First release meant to be installed by someone other than me. What changed:

- **Windows updates itself.** One click on "download and install": fetch, verify the
  checksum, swap the running executable and relaunch. No unzipping, no manual replace.
- **macOS ships a disk image.** Download the `.dmg`, drag the app to Applications and
  run it. The in-app update opens the `.dmg` download and stops there — installing is
  yours to do.
- **Fixed: the app would not start at all on Windows.** The embedded manifest declared
  a three-part assembly version, which Windows rejects, so the process was killed by
  the loader before it could draw anything. Doubled-clicking did nothing and left no
  message behind.
- **Fixed: closing the main window left a windowless process running**, and the next
  double-click was swallowed by the single-instance check — it looked like the app was
  dead. Closing the main window now exits.
- **Fixed macOS arm64 builds were never produced**, so Apple Silicon users had nothing
  to download. Both architectures are now built from one job.

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

macOS:

1. Download the `deskkit-<version>-darwin-<arch>.dmg` that matches your Mac
   (`arm64` for Apple Silicon, `amd64` for Intel), open it and drag the app to
   Applications.
2. First launch: Gatekeeper will refuse an unsigned app — right-click the app,
   choose **Open**, then confirm.
3. The in-app update downloads the newer `.dmg` for you; drag-and-drop is still manual.

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
- The Windows updater stages the new build inside the app's own folder, so the app must
  live somewhere writable (anywhere under your user profile is fine). Under
  `C:\Program Files` it cannot update itself.

## Verify

`SHA256SUMS` is attached to this release. Checksums protect against a corrupted
download; they are not a signature.

---

MIT licensed. Source, issues and discussion: https://github.com/klphp/deskkit-excel
