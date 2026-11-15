---
name: morse-notify
description: Send, update and read back Telegram messages with the `morse` CLI. Use when the user asks to be notified, texted, pinged or messaged — a long build or job finishing, a failure, a result worth seeing while they are away — when a long-running task should keep one status message current, when a script or job needs to report to them, or when they want something they sent to the bot (text, a key, a file) fetched into the terminal.
---

# Telegram messages with morse

`morse` sends one Telegram message and exits. The caller decides when there is
something to say; morse delivers it, and never watches, polls or schedules.

## Before the first send: is it configured?

```sh
morse capabilities --json
```

It answers even when nothing is set up, so check with it rather than sending a
probe message. `.delivery.configured` is `false` when credentials are missing
and `.config_error` says why. It also lists every flag and limit, so it is the
authority if this page and the installed version disagree; `.version` names
the release.

Credentials come from `~/.config/morse/config.yaml`, or `MORSE_BOT_TOKEN` and
`MORSE_CHAT_ID`, which win over the file — setting those two is the whole
setup in a container or under an agent. Never print or echo the token.
`--config <path>` reads another file.

## Send

```sh
morse send "Backup failed" "rsync exit 23"        # title + body
morse send --title "$title" --body "$body"        # named, for values from variables
morse send --silent "Nightly ok" "412 files"      # delivered without a sound
tail -n 40 build.log | morse send --code "Build failed"   # body from stdin, as code
```

Rules that bite:

- **Flags come before the title.** `morse send "title" --silent` is an error,
  not a silent send.
- Prefer `--title`/`--body` when values come from variables: they stay
  unambiguous when one is empty or starts with a dash.
- The body is read from stdin only when something is piped in. With no body
  and no pipe, a titled send still goes out, with "(no details)".
- Text is sent as Telegram HTML and escaped for you — do not pre-escape, and do
  not expect Markdown to render. It is trimmed to fit (4096 characters, 1024
  for a file caption); the body gives way before the title.
- Exit status: 0 delivered, 1 failed, 2 unknown command. Errors go to stderr
  and never contain the token. If Telegram says "too many requests", morse
  already waited and retried once — do not wrap it in a retry loop.

## How it looks

- `--code` shows the body as a monospace block: use it for logs, stack traces,
  command output. `--collapse` folds a long body behind "show more". Both
  together suit a journal excerpt.
- `--no-preview` stops the first URL becoming a large preview card.
- `--photo <path>` shows a screenshot or graph inline (10 MB max). `--file
  <path>` uploads anything as a document (50 MB max). Hand either a path, never
  file contents; the title and body become the caption.

## Where it goes

- `--topic <id|name>` posts into one forum topic of the chat, so each app keeps
  its own thread. Names come from `telegram.topics` in the config; an unknown
  name is refused, never sent to General. `morse topic create <name>` makes a
  topic and prints its id (the bot needs the right to manage topics).
- `--reply-to <id|label>` attaches the message to an earlier one, so "finished"
  sits under "started". A number is an id; anything else is a `--track` label.
- `--pin` pins the message silently. If pinning is refused the message was
  still delivered — the exit status is 1 and stderr says it was not pinned.

## Long-running work: one status line, not fifty messages

```sh
morse send --track deploy --silent "Deploy" "● 1/5 hosts"   # the same call
morse send --track deploy --silent "Deploy" "● 3/5 hosts"   # every time
morse send --track deploy --silent "Deploy" "✓ done in 4m"
```

- `--track <label>` makes the label stand for one message: sent the first
  time, edited in place after, re-sent if someone deleted it. **Make the same
  call every time**; never branch on whether you have reported before.
- An edit never notifies anyone — that is Telegram, not a flag. Only the first
  send can make a sound, so pass `--silent` if even that is unwanted.
- Pass the same `--topic`, `--code`, `--collapse` and `--no-preview` on every
  call: a different topic starts a new message there, and a rewrite without the
  formatting flags drops the formatting. `--pin` pins only when the message is
  new, so it is safe to pass every time.
- An upload (`--file`, `--photo`) cannot be edited, so a tracked upload is a
  new message each time.
- `morse delete --track <label>` removes the status when it is no longer
  useful, and forgets the label. `morse delete <id>...` removes by id.
- `morse edit --track <label> ...` rewrites but fails if the label means
  nothing yet — for interactive use, where a typo should say so. In scripts,
  use `send --track`. `--json` on send or edit prints the message id.
- morse cannot tell what a message says now or whether it was read; the Bot API
  has no way to ask. The label file is only morse's record of what it did.

## Reading back what the user sent

```sh
morse receive list                 # newest first, message id first on each line
morse receive get 481              # its text on stdout, no trailing newline
morse receive get 481 --save ~/Downloads   # the file it carried
```

This is for "get me the file / key / command I just sent the bot". It shows
only messages sent *to* the bot — never morse's own notifications. The window
is what Telegram still holds: up to 100 updates, roughly a day. An edited
message shows its current text; `--json` gives one object per line. Reading
never consumes anything. To put text on a clipboard, pipe `get` to `wl-copy`,
`xclip` or `pbcopy`; morse does not reach for one.

## Choosing how to send

- **Routine facts** — a job succeeded, a nightly finished: `--silent`, and
  `--track` if the same thing reports repeatedly. Buzzing for these trains the
  reader to mute the chat, which costs the messages that matter.
- **Something is wrong, or waiting on the user**: loud (no `--silent`), its own
  message (no `--track`).
- The *what* goes in the title — it is what a lock screen shows. The evidence
  goes in the body: the log tail, the exit code, the diff stat.
- Send once per event. A loop that notifies per iteration should either decide
  first and speak once, or `send --track` so the chat keeps one current line.

## Do not

- Message the user to announce your own progress unless they asked to be
  notified — it goes to their phone.
- Build watching, polling or scheduling into morse, or loop on `morse receive`.
  Deciding *when* to speak belongs to the caller.

## Calling it from a script

```sh
if ! out=$(rsync -a "$src" "$dst" 2>&1); then
    printf '%s\n' "$out" | tail -n 40 | morse send --code --collapse "Backup failed"
fi
```
