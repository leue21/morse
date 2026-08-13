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
main.go              → subcommand dispatch (send, edit, receive, capabilities, help) and send/edit
receive.go           → `morse receive list|get`: read the chat back, to stdout or a file
capabilities.go      → `morse capabilities`: the callable interface, as text or JSON
config/              → telegram credentials, from the file or MORSE_BOT_TOKEN / MORSE_CHAT_ID
notifier/            → Telegram Bot API client (Send, SendDocument, Edit, Updates, Download)
track/               → which message a `--track` label stands for, under ~/.local/state/morse
internal/testutil/   → FakeAPI HTTP mock for tests
```

Anything that needs to watch something is a separate job that calls `morse
send`, not code inside morse. Adding scheduling, watching, or a long-running
mode to this repo is out of scope: the caller has the context to decide when
there is something worth reporting, and morse only has to say it.

`receive` does not change that either. It is one call that returns what Telegram
is currently holding and then exits: no polling loop, no cursor, no archive. The
temptation it invites is exactly the out-of-scope one — confirm an offset, keep
a store, watch for replies — and that is a different program. See the note on
the window under `notifier.Updates` before touching any of it: **no offset may
be sent, of either sign**. A positive one deletes every update below it, and a
negative one is documented as "all previous updates will be forgotten" — both
server-side, permanently, for every reader of that bot rather than just for
morse. Neither is something a test on this side will catch. `allowed_updates`
is left unsent for the same reason: Telegram remembers it and applies it to
every other client polling the bot.

Telegram also withholds updates it has just delivered: a second `getUpdates`
within about half a second is answered with the queue's oldest update alone,
and the rest are back once that passes. Nothing is consumed and no offset is
confirmed — they are simply not offered twice that fast. `findMessage` in
`receive.go` exists for this, and is why `list | fzf | xargs get` works at all;
do not "simplify" it into a single poll.

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
- `receive list` prints the message id first on every line, unpadded. That is
  the whole contract with a fuzzy finder — `list | fzf | cut -d' ' -f1 | xargs
  morse receive get` — so nothing goes in front of it. `get` re-reads the window
  by id rather than parsing a line back, which is what lets any picker sit in
  the middle
- A filename from `--save` was chosen by whoever sent the file, so it is
  validated like a `--track` label: `notifier.destination` refuses anything that
  is a path rather than a name, instead of quietly flattening it to its base.
  The download is completed under a temporary name, then published with an
  atomic hard link that refuses a name already taken; filesystems without hard
  links fall back to an `O_EXCL` copy, preserving the no-overwrite rule
- `receive get` writes the text with no trailing newline. A newline on the end
  of a pasted command line is a keystroke that runs it, and the point of the
  command is that its output gets pasted
- `receive get` writes text to stdout and stops there. It had a `--copy` that
  reached the clipboard itself — five programs, a `PATH` lookup, a subprocess,
  `DISPLAY`/`SSH_CONNECTION` sniffing and an OSC 52 fallback for ssh — and it
  came to 392 lines around 15 lines of useful work. It was deleted: which
  clipboard to write to depends on the display server and on which end of an
  ssh connection the person is sitting, morse can only guess at both, and a
  pipe knows the answer. The README documents the one-liners it replaced; do
  not add the flag back
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
