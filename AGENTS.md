# AGENTS.md

Guidance for coding agents working in this repository.

## Build & Test

Go lives at `/usr/local/go/bin/go`. Use the Makefile:

```bash
make build          # compile binary
make test           # run all tests (-v -count=1)
make vet            # static analysis
make clean          # remove binary + coverage.out
make install        # install binary + systemd user service + config
```

Run a single test:

```bash
/usr/local/go/bin/go test ./config -run TestLoadFromEnvironmentWithoutAFile -v
```

## Architecture

**morse** is a CLI that sends a Telegram message. No daemon, no scheduler, no
plugins — callers decide when to speak. Minimal dependencies (only
`gopkg.in/yaml.v3`; everything else is stdlib).

```
main.go              → subcommand dispatch (send, capabilities, help) and the send command
capabilities.go      → `morse capabilities`: the callable interface, as text or JSON
config/              → telegram credentials, from the file or MORSE_BOT_TOKEN / MORSE_CHAT_ID
notifier/            → Telegram Bot API client (Send)
internal/testutil/   → FakeAPI HTTP mock for tests
```

Anything that needs to watch something is a separate job that calls `morse
send`, not code inside morse. The [diskguard](https://github.com/leue21/diskguard)
repo is the reference for that shape.

## Conventions

- `--silent` maps to Telegram's `disable_notification`; the message still arrives
- Errors are wrapped with `fmt.Errorf("context: %w", err)`; the CLI exits
  non-zero and prints to stderr — once, from `main`, so flag sets use
  `ContinueOnError` with their output discarded
- Errors must never carry the bot token: it lives in the request URL, and a
  transport error stringifies that URL. `notifier.withoutURL` strips it
- Messages go out as Telegram HTML, escaped with stdlib `html.EscapeString`.
  MarkdownV2 reserves twenty-odd characters and one missed escape makes the API
  reject the message, losing the alert; HTML reserves three, and stdlib owns them
- All HTTP calls use `http.NewRequestWithContext(ctx, ...)` for cancellation; the
  context comes from `main` and is cancelled on SIGINT/SIGTERM
- Tests use `testutil.FakeAPI` for HTTP mocking; `notifier.Telegram` exposes an
  internal `baseURL` for that
