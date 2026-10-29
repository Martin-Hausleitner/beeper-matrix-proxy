# Voice AI transcription

This bridge can post local transcript/summary replies for selected voice
messages after they have landed in Matrix. It covers both directions:

- Beeper/PIPA source voice notes mirrored into Matrix.
- Matrix-origin `m.audio` messages that are forwarded from a Matrix portal to
  Beeper.

The feature is disabled by default and fails closed: enabling it without a
positive chat or sender allowlist does nothing.

## Recommended Mac stack

For Apple Silicon, use MLX Whisper for speech-to-text and LM Studio's
OpenAI-compatible API for transcript cleanup and German summaries.

Install the local tools:

```bash
brew install ffmpeg
```

Example transcription command using MLX Whisper:

```bash
export BEEPER_MATRIX_PROXY_VOICE_AI_TRANSCRIBE_COMMAND='outdir=$(mktemp -d); trap '\''rm -rf "$outdir"'\'' EXIT; uvx --from mlx-whisper mlx_whisper "$AUDIO_PATH" --model mlx-community/whisper-large-v3-turbo --output-dir "$outdir" --output-format txt --verbose False >/dev/null 2>&1 && cat "$outdir"/*.txt'
```

LM Studio local API defaults to:

```bash
export BEEPER_MATRIX_PROXY_VOICE_AI_SUMMARY_BASE_URL='http://127.0.0.1:1234/v1'
export BEEPER_MATRIX_PROXY_VOICE_AI_SUMMARY_MODEL='matrix-summarizer'
export LM_STUDIO_API_KEY='local'
```

`matrix-summarizer` is an LM Studio identifier, not a required upstream model
name. On a 24 GB Apple Silicon Mac, start with a quantized Qwen3 8B/14B or
Gemma 3 12B model. Load it in LM Studio with that identifier before enabling
the bridge feature.

If the `lms` CLI is installed, the bridge can run start/stop hooks around each
voice job:

```bash
export BEEPER_MATRIX_PROXY_VOICE_AI_START_COMMAND='lms server start >/dev/null 2>&1 || true'
export BEEPER_MATRIX_PROXY_VOICE_AI_STOP_COMMAND='lms server stop >/dev/null 2>&1 || true'
```

If keeping LM Studio warm is faster for your machine, leave the start/stop
commands empty and run the server yourself.

## Safe allowlist example

The identity allowlist is intentionally separate from the bridge's broad chat
mirror allowlist.

```bash
export BEEPER_MATRIX_PROXY_VOICE_AI_ENABLED=true
export BEEPER_MATRIX_PROXY_VOICE_AI_ALLOW_NETWORKS='Signal,WhatsApp'
export BEEPER_MATRIX_PROXY_VOICE_AI_ALLOW_CHAT_IDS='!exact-signal-test-chat-id:beeper,!exact-whatsapp-test-chat-id:beeper'
export BEEPER_MATRIX_PROXY_VOICE_AI_LANGUAGE=auto
export BEEPER_MATRIX_PROXY_VOICE_AI_MAX_AUDIO_BYTES=26214400
export BEEPER_MATRIX_PROXY_VOICE_AI_COMMAND_TIMEOUT_SECONDS=300
```

Exact Beeper chat IDs are required for the safest activation profile:

```bash
sqlite3 -header -column beeper-source-all-chats.db \
  "SELECT beeper_chat_id, matrix_room_id, account_id FROM portal ORDER BY account_id, beeper_chat_id;"
```

Exact chat IDs are the recommended allowlist for Matrix-origin audio, because
the live Matrix listener can reliably resolve portal ID/account/network, but not
always the human room display name.

For the first live dry run, keep Matrix-to-Beeper disabled so the listener can
only observe Beeper-source voice notes and post Matrix transcript replies:

```bash
export BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER=true
```

Remove that line only after a real test voice note has produced exactly one
marked Matrix `m.notice` reply.

The dedicated `run-beeper-source-voice-ai.sh` runner defaults this switch to
`true`. Set it explicitly to `false` only when you want Matrix-origin audio in
portal rooms to be forwarded to WhatsApp/Signal as well.

## Live replay proof

Existing local Beeper raw voice messages can be replayed through the same
processor without re-sending the original audio to Beeper:

```bash
export BEEPER_SOURCE_DB="$PWD/beeper-source-all-chats.db"
export BEEPER_MATRIX_PROXY_VOICE_AI_LIVE_REPLAY_IDS='166223,166226'
export BEEPER_MATRIX_PROXY_VOICE_AI_LIVE_REPLAY_SUFFIX="$(date +%Y%m%d%H%M%S)"
go test ./beepersource -run TestVoiceAILiveReplay -count=1 -v
```

The suffix forces fresh Matrix transaction IDs, which is useful when proving a
new local model after an older proof event was redacted or deduplicated.

## Listener runner

The existing `run-bridge.sh` starts the Bridge-v2 Beeper bridge through
`bbctl`. Voice AI runs in the separate `beeper-source` listener:

```bash
./run-beeper-source-voice-ai.sh
```

The runner loads `.env` first and then an optional local `voice-ai.env`. Keep
real chat IDs and private local model choices in `voice-ai.env`, not in public
docs or committed config.

Example `voice-ai.env`:

```bash
BEEPER_MATRIX_PROXY_VOICE_AI_ENABLED=true
BEEPER_MATRIX_PROXY_VOICE_AI_ALLOW_CHAT_IDS='!signal-room:beeper,!whatsapp-room:beeper'
BEEPER_MATRIX_PROXY_VOICE_AI_ALLOW_NETWORKS='Signal,WhatsApp'
BEEPER_MATRIX_PROXY_VOICE_AI_TRANSCRIBE_COMMAND='outdir=$(mktemp -d); trap '\''rm -rf "$outdir"'\'' EXIT; uvx --from mlx-whisper mlx_whisper "$AUDIO_PATH" --model mlx-community/whisper-large-v3-turbo --output-dir "$outdir" --output-format txt --verbose False >/dev/null 2>&1 && cat "$outdir"/*.txt'
BEEPER_MATRIX_PROXY_VOICE_AI_SUMMARY_BASE_URL='http://127.0.0.1:1234/v1'
BEEPER_MATRIX_PROXY_VOICE_AI_SUMMARY_MODEL='matrix-summarizer'
LM_STUDIO_API_KEY='local'
```

## Runtime behavior

- The original voice message is mirrored/forwarded first.
- The transcript reply uses `m.notice` and Matrix reply metadata pointing at the
  original voice event.
- The reply includes `com.openclaw.voice_ai` metadata so the Matrix source
  ignores it and does not forward it back to WhatsApp, Signal, or Beeper.
- A durable SQLite key deduplicates processing by Beeper message ID and message
  version, including failed optional AI jobs so a bad local model does not block
  the bridge loop.
- E2EE Matrix media is not decrypted by this path; it works on Beeper-source
  media that the bridge can download from the local Beeper Desktop API.
