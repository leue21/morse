---
name: morse-notify
description: Send a Telegram notification from the command line with the `morse` CLI. Use when the user asks to be notified, texted, pinged, or messaged about something — a long build finishing, a failing job, a result worth surfacing while they are away — or when a script or job needs to report to them.
---

# Sending a notification with morse

`morse` sends one Telegram message and exits. The caller decides when there is
something to say.

## Send

```sh
morse send "Backup failed" "rsync exit 23"       # title + body
morse send "Deploy finished"                     # title only; body becomes "(no details)"
morse send --silent "Nightly job ok" "412 files" # delivers without a sound
tail -n 30 build.log | morse send "Build failed"  # body from stdin
morse send --file report.pdf "Nightly report"    # upload a file
```

Rules that will bite you otherwise:

- **Flags come before the title.** `morse send "title" --silent` is an error,
  not a silent send — anything after the title would land in the message body.
- The body is read from stdin only when something is actually piped in. At a
  terminal, a title-only send returns immediately rather than hanging.
- With no body argument *and* no pipe, a titled send still goes out.
- Message text is sent as Telegram HTML and escaped for you — do not
  pre-escape, and do not expect Markdown to render.
- `--file <path>` uploads that file, with the title and body as its caption.
  A caption holds 1024 characters (a message holds 4096) and morse trims the
  body to fit; hand it a path, never file contents. Uploads over 50 MB are
  refused before anything is sent.
- Exit status is 0 on delivery, 1 on any failure (bad config, network,
  Telegram rejection), 2 on an unknown command or no arguments. Errors go to
  stderr and never contain the bot token.

## Updating instead of sending again

When something long-running reports repeatedly — progress, a job's state,
a queue draining — send once and rewrite that message, rather than adding a
line to the chat each time:

```sh
morse send --track backup --silent "Backup" "0%"   # once, at the start
morse edit --track backup "Backup" "40%"           # as often as you like
morse edit --track backup "Backup" "done — 3m21s"
```

- **An edit never notifies anyone.** That is the Bot API's behaviour, not a
  flag, so `edit` has no `--silent` and there is nothing to suppress. Only the
  first `send` can make a sound.
- `--track <label>` lets the next run find the message: morse writes the id
  under that label in `~/.local/state/morse`. Editing a label that was never
  sent is an error — send it first.
- If the tracked message was deleted, `edit --track` silently sends a new one
  and repoints the label. An explicit id (`morse edit 4242 ...`) means that one
  message and fails instead.
- `morse send --json` prints the message id, for a caller that would rather
  hold it than use a label: `id=$(morse send --json "Backup" "0%" | jq .message_id)`.
- morse cannot tell you what a message currently says or whether it was read —
  the Bot API has no way to ask. The label file is morse's own record of what
  it last sent.
- `--file` cannot be edited.

## Before sending: is it configured?

```sh
morse capabilities --json
```

It answers even when nothing is set up, so use it to check rather than sending
a probe message. `.delivery.configured` is `false` when credentials are missing
and `.config_error` says why; `.version` names the release, which `morse
version` also prints on its own. Both commands accept `--config <path>` to read
credentials from somewhere other than `~/.config/morse/config.yaml`.

Credentials come from that file or from `MORSE_BOT_TOKEN` / `MORSE_CHAT_ID`,
which win over the file. In a container or under an agent, setting the two
variables is the whole setup — never print or echo the token.

## Choosing how to send

- **Routine facts** — a job succeeded, a nightly finished — use `--silent`.
  Buzzing for these trains the reader to mute the chat, which costs the
  messages that matter.
- **Something is wrong or waiting on the user** — send loud (no flag).
- Put the *what* in the title and the evidence in the body. Titles are what
  gets read on a lock screen; bodies are for the log tail, the exit code, the
  diff stat.
- Send once per event. A loop that notifies per iteration is a job that should
  decide first and speak once — or one that should `send --track` once and
  `edit --track` after that, so the chat keeps one current line instead of a
  history.

## Do not

- Send on the user's behalf to announce your own progress unless they asked to
  be notified — a message goes to their phone.
- Build watching, polling, or scheduling into morse. morse sends one message and
  exits; deciding *when* there is something to say belongs to the caller.

## Calling it from a script

The caller decides there is something to report, then hands morse the text:

```sh
if ! out=$(rsync -a "$src" "$dst" 2>&1); then
    morse send "Backup failed" "$out"
fi
```
