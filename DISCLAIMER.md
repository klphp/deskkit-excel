# Disclaimer

**This software is provided "AS IS", without warranty of any kind.**

- **Source available**: the source is in this repository — audit it, build it, fork it.
- **Unsigned builds**: Windows SmartScreen and macOS Gatekeeper may warn about an
  unknown developer. That is what unsigned means, not a sign of a problem.
- **No warranty**, including fitness for a particular purpose or freedom from data loss.
  **Back up important data before use.**
- **Data flow**: file processing happens locally; the app itself uploads nothing.
  If you configure a cloud model provider, your messages and the results returned by
  the tools (which can contain table data) are sent to that provider. With a local
  model, nothing leaves the machine. The only other outbound request is the update check.
- **AI output can be wrong**: the app shows pending changes and asks for confirmation
  before writing, but verify the results yourself — especially amounts, IDs and dates.
- **No support commitment**: issues and suggestions are welcome, but no response or
  fix time is promised.

Third-party components keep their own licenses. Notably SQLCipher
(github.com/mutecomm/go-sqlcipher) — Copyright (c) Zetic LLC, BSD-style license.
