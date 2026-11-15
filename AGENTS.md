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

Against the real Bot API, in a test group (opt-in, never in CI):

```bash
MORSE_SMOKE_CHAT_ID=-100… make smoke
```

The race detector does not run on this Raspberry Pi (its kernel's 47-bit
address space is unsupported); CI runs `go test -race` on every push and PR.

## Architecture

**morse** is a CLI that sends one Telegram message and exits; callers decide
when to speak. Minimal dependencies (only `gopkg.in/yaml.v3`; everything else is
stdlib).

```
main.go              → subcommand dispatch (send, edit, receive, delete, topic, capabilities, help) and send/edit
delete.go            → `morse delete`: remove messages by id, or a label's message and the label
receive.go           → `morse receive list|get`: read the chat back, to stdout or a file
topic.go             → `morse topic create`: open a forum topic, print its id
capabilities.go      → `morse capabilities`: the callable interface, as text or JSON
config/              → telegram credentials, from the file or MORSE_BOT_TOKEN / MORSE_CHAT_ID
notifier/            → Telegram Bot API client (Send, SendDocument, Edit, Updates, Download)
track/               → which message a `--track` label stands for, under ~/.local/state/morse
internal/testutil/   → FakeAPI HTTP mock for tests
cli_test.go          → whole commands against a fake API, via MORSE_API_URL
smoke_test.go        → `make smoke`: the real API, in a test group (build tag)
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
- `send --topic <id>` maps to `message_thread_id` and is how one chat keeps
  several apps apart; telling them apart is the chat's job, not `receive`'s,
  since a bot never sees its own messages. How a message is delivered rather
  than what it says goes in `notifier.SendOptions`, so a new delivery option is
  a field there, not another positional argument on `Send`. `message_thread_id`
  is left out when there is no topic, never sent as 0 — the General topic is the
  chat itself and takes none
- `--topic` takes an id or a name from `telegram.topics` in the config, which is
  validated on load; a number is always an id, so a name may not be one. A bot
  cannot list topics, so names cannot be looked up and the config is where they
  live — `morse topic create` prints the lines to add rather than rewriting a
  file that holds the token and the owner's comments
- A 429 is waited out once, for the `retry_after` it names, up to
  `maxRetryWait`; `notifier.call` takes the body as bytes so it can be sent
  twice. Never in a loop: a limit that outlasts its own wait is reported
- `--pin` pins only a message that is new, never a rewrite. A failed pin leaves
  a delivered message, so the label is recorded and the id printed before the
  error. `delete --track` forgets the label only after the delete succeeded
- `--reply-to` and `delete --track` refuse a label recorded in another chat:
  the id there names an unrelated message
- `receive` merges `edited_message` updates into the original's place, and
  names what a pin notice pinned, so neither shows as a stale or blank line
- `--code`, `--collapse` and `--no-preview` are a `notifier.Format`, which `Edit` takes as well
  as `Send`: a rewrite replaces the markup with the text. The format applies to
  the body only — the title is what a lock screen shows, and it renders no
  markup. The tags go on after escaping and truncation, and are not counted
  against the limit, since Telegram counts text with the entities parsed out
- A `--track` record keeps its topic, because `edit --track` takes no `--topic`
  and the record is the only thing that knows where a replacement belongs. A
  message cannot move between topics, so `send --track` with a different topic
  than the record's starts a new message there rather than rewriting the old one.
  The same goes for the chat: a message id is only unique within its chat, so a
  record from another chat is treated as unknown by `send --track`, and refused
  by `edit --track`, rather than editing whatever carries that id in the new one
- In `receive`, `message_thread_id` is a topic only when `is_topic_message` is
  set: Telegram also sets it on a plain reply, naming the reply thread
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
- Tests use `testutil.FakeAPI` for HTTP mocking. A notifier test can set the
  internal `baseURL`; anything testing a whole command goes through
  `MORSE_API_URL` (`newFake` in `cli_test.go`), the same setting a self-hosted
  server uses, so the code under test is the code that ships
- A rule about what a command does — edit or send, resend or fail, which topic
  — gets a test in `cli_test.go`, not only one on the helper that implements
  it: the helpers were each tested before, and still nothing checked that the
  command called them
