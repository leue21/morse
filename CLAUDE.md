# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

Go binary is at `/usr/local/go/bin/go`. Use the Makefile:

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
contrib/diskguard/   → an example consumer: a systemd timer that calls `morse send`
```

Anything that needs to watch something is a separate job that calls `morse
send`, not code inside morse. contrib/diskguard is the reference for that shape.

## Conventions

- `--silent` maps to Telegram's `disable_notification`; the message still arrives
- Errors are wrapped with `fmt.Errorf("context: %w", err)`; the CLI exits non-zero and prints to stderr
- Logging via `log/slog`; per-plugin loggers use `slog.With("plugin", name)`
- All HTTP calls use `http.NewRequestWithContext(ctx, ...)` for cancellation
- Each plugin creates its own `http.Client` with appropriate timeouts
- Tests use `testutil.FakeAPI` for HTTP mocking; plugins expose internal URLs/fields for testability
