# AGENTS.md

Guidance for coding agents working in this repository.

## Build & Test

Go lives at `/usr/local/go/bin/go`. Use the Makefile:

```bash
make build          # compile binary
make test           # run all tests (-v -count=1)
make vet            # static analysis
make clean          # remove binary + coverage.out
make install        # install binary + example config
```

Run a single test:

```bash
/usr/local/go/bin/go test ./config -run TestLoadFromEnvironmentWithoutAFile -v
```

## Architecture

**morse** is a CLI that sends one Telegram message and exits; callers decide
when to speak. Minimal dependencies (only `gopkg.in/yaml.v3`; everything else is
stdlib).

```
main.go              → subcommand dispatch (send, edit, capabilities, help) and those commands
capabilities.go      → `morse capabilities`: the callable interface, as text or JSON
config/              → telegram credentials, from the file or MORSE_BOT_TOKEN / MORSE_CHAT_ID
notifier/            → Telegram Bot API client (Send, SendDocument, Edit)
track/               → which message a `--track` label stands for, under ~/.local/state/morse
internal/testutil/   → FakeAPI HTTP mock for tests
```

Anything that needs to watch something is a separate job that calls `morse
send`, not code inside morse. Adding scheduling, watching, or a long-running
mode to this repo is out of scope: the caller has the context to decide when
there is something worth reporting, and morse only has to say it.

`track/` does not change that. It is a lookup table kept on the caller's
behalf — the same message id it could have stored itself — written during a run
and read at the start of the next one. Nothing in it runs, watches, or expires,
and morse never acts on it without being asked. It also never reports anything
Telegram said: the Bot API has no way to fetch a message's current content or
whether it was read, so a record only ever says what morse itself last did.

## Conventions

- `--silent` maps to Telegram's `disable_notification`; the message still arrives
- An edit never notifies, by API behaviour rather than by flag, so `morse edit`
  takes no `--silent`. Do not add one: it would imply a louder alternative that
  does not exist
- `send --track <label>` sends or rewrites depending on what the label already
  means, so a caller can make one call every time and never branch on whether
  it has reported before. `edit --track` is the strict form and fails on a
  label that means nothing yet — keep it that way, it is what makes a mistyped
  label say so instead of quietly starting a second line
- The Bot API says *why* it refused only in prose, in the description of a 400.
  `notifier.refusal` is the one place that reads that prose, turning the phrases
  morse can act on into sentinels (`ErrMessageGone`, `ErrNotModified`); add a
  case there rather than matching on an error's text somewhere else. `notifier`
  reports which refusal it was, and `main` decides what to do about it — an
  unchanged edit is a success, a gone message with a label is re-sent
- A `--track` label becomes a filename, so it is validated before it is joined
  to a path, and one label is one file — two callers never share a file and so
  need no lock. Writes go through a temp file and a rename, because a truncated
  record strands the message it names
- Errors are wrapped with `fmt.Errorf("context: %w", err)`; the CLI exits
  non-zero and prints to stderr — once, from `main`, so flag sets use
  `ContinueOnError` with their output discarded
- Errors must never carry the bot token: it lives in the request URL, and a
  transport error stringifies that URL. `notifier.withoutURL` strips it
- Text is trimmed to Telegram's limits (4096 for a message, 1024 for a file
  caption) rather than sent whole and rejected; the body gives way before the
  title, and the cut happens before escaping so it cannot land inside an `&amp;`
- Messages go out as Telegram HTML, escaped with stdlib `html.EscapeString`.
  MarkdownV2 reserves twenty-odd characters and one missed escape makes the API
  reject the message, losing the alert; HTML reserves three, and stdlib owns them
- All HTTP calls use `http.NewRequestWithContext(ctx, ...)` for cancellation; the
  context comes from `main` and is cancelled on SIGINT/SIGTERM
- Tests use `testutil.FakeAPI` for HTTP mocking; `notifier.Telegram` exposes an
  internal `baseURL` for that
