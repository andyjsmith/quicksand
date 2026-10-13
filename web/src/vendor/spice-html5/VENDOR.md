# Vendored spice-html5

- Upstream: https://gitlab.freedesktop.org/spice/spice-html5
- Commit: b372aab6e5ab3ce056f486d3bdcd3845f9e34aad ("Remove the ES module polyfill", 2026-09-21)
- Files: the upstream `src/` directory, plus `COPYING` and `COPYING.LESSER`.
- License: LGPL-3.0-or-later (thirdparty/ files carry their own BSD-style notices).

## Local changes

- `thirdparty/rng.js`: seed the RNG pool from `crypto.getRandomValues` instead of
  only `Math.random()`. The pool supplies the random padding when the ticket is
  RSA-encrypted.
- `cursor.js`: ignore `SPICE_MSG_CURSOR_HIDE` in client mouse mode while the
  pointer is within 8 px of the left edge. Windows guests send a hide there
  and never re-show the pointer until its shape changes.
- `cursor.js`: apply the cursor shape carried by `SPICE_MSG_CURSOR_INIT`.
- `playback.js`: hand playback messages to `parent.playback_sink` when the
  application sets one. Quicksand decodes the Opus packets with WebCodecs and
  plays them with Web Audio, because Firefox cannot play the live WebM/MSE
  stream: whenever its demuxer catches up with the live edge it drains the
  FFmpeg Opus decoder, which then reports end of stream and playback ends.

## Quicksand additions (not upstream)

- `*.d.ts`: TypeScript declarations for the parts of the API Quicksand uses.
