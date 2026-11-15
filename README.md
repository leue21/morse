# morse

Send Telegram notifications from the command line. morse is a small,
single-binary Go CLI that scripts, cron jobs, CI pipelines and AI agents use to
send alerts to a Telegram chat, keep a status message up to date, upload files,
and read replies back.

```sh
morse send "Backup failed" "rsync exit 23"
echo "$logs" | morse send "Build failed on main"
morse send --silent "Backup finished" "412 files, 3m21s"
morse send --title "Backup failed" --body "$out"
morse send --file report.pdf "Nightly report"
morse send --track nightly "Nightly backup" "✓ done — 412 files"
morse send --topic ci "CI" "main is red"
journalctl -u backup -n 40 | morse send --code --collapse "Backup failed"
morse receive list | fzf | cut -d' ' -f1 | xargs morse receive get
```

morse sends one message and exits. Anything that wants to be told something
decides for itself when to speak, and calls morse to do the speaking: a script,
a cron job, an agent, a person at a prompt.

It reads the other direction the same way — one call, no daemon — so what was
sent back to the bot can be found and used from the terminal it was meant for.

## Install

```sh
make install
```

Installs the binary under `~/.local/bin` and — only if there is not one already
— an example `~/.config/morse/config.yaml` to fill in. No step needs root.

### Go

```sh
go install github.com/leue21/morse@latest
```

Installs the binary into `$(go env GOPATH)/bin`. Copy `config.yaml.example` to
`~/.config/morse/config.yaml`, or set `MORSE_BOT_TOKEN` and `MORSE_CHAT_ID`.

### Homebrew

The repository is its own tap, so there is no second repo to add:

```sh
brew tap leue21/morse https://github.com/leue21/morse
brew install leue21/morse/morse
```

The name has to be given in full. Homebrew's own core tap has an unrelated
`morse` — a morse code trainer — and a bare `brew install morse` installs that
one instead. The same goes for `brew upgrade leue21/morse/morse` and
`brew uninstall leue21/morse/morse`.

To follow `main` instead of the latest tag, add `--HEAD`.

Homebrew installs the binary and leaves the config alone — a package manager
has no business writing to `~/.config`. The example lands in the formula's
share directory, and `brew install` prints how to copy it:

```sh
mkdir -p ~/.config/morse
cp "$(brew --prefix)/share/morse/config.yaml.example" ~/.config/morse/config.yaml
chmod 600 ~/.config/morse/config.yaml
```

Or set `MORSE_BOT_TOKEN` and `MORSE_CHAT_ID` and skip the file entirely.
Uninstalling leaves the config in place.

## Configuration

Credentials go in `~/.config/morse/config.yaml`:

```yaml
telegram:
  bot_token: "123456:ABC-DEF..."   # from @BotFather
  chat_id: 12345678                # your chat/group ID
```

Or in the environment, which wins over the file:

```sh
export MORSE_BOT_TOKEN=123456:ABC-DEF...
export MORSE_CHAT_ID=12345678
```

With both variables set, no config file is needed at all — which is the
difference between "install and configure this" and "set two variables", and
matters for anything running in a container or under an agent.

### A self-hosted Bot API server

```yaml
telegram:
  api_url: http://localhost:8081   # or MORSE_API_URL
```

`api_url` points morse at a Bot API server other than Telegram's, such as a
self-hosted [telegram-bot-api](https://github.com/tdlib/telegram-bot-api),
which accepts uploads up to 2000 MB rather than 50. The bot token is sent to
whatever it names, so set it only to a server you run.

### Getting Telegram credentials

1. Message [@BotFather](https://t.me/BotFather) to create a bot and get the `bot_token`
2. Send a message to your bot, then fetch `https://api.telegram.org/bot<TOKEN>/getUpdates` to find your `chat_id`

For a group with one topic per app, see [One topic per app](#one-topic-per-app).

## Usage

```
morse send [--silent] [--file path] <title> [body]   send a message
morse send --title <t> --body <b>                    the same, named
morse send --topic <id|name> <title> [body]          into one topic of the chat
morse send --code [--collapse] <title> [body]        body as a code block, folded
morse send --track <label> [--json] <title> [body]   send, and remember it
morse edit <message_id> <title> [body]               rewrite a sent message
morse edit --track <label> [--json] <title> [body]   the same, by label
morse topic create [--json] <name>                   open a topic, print its id
morse capabilities [--json]                          what morse accepts
morse version
morse help
```

`send`, `edit` and `capabilities` take `--config <path>` to read credentials from
somewhere other than `~/.config/morse/config.yaml`. Flags come before the
title; a flag after it would otherwise end up in the message body.

With no body argument the body is read from stdin when something is piped in,
so a caller can pass a log excerpt that way. A title on its own still sends: a
job that died without logging is exactly the case worth hearing about.

### Naming the title and body

```sh
morse send --title "Backup failed" --body "rsync exit 23"
morse send --title "Diff" --body "-3 lines"
morse send --title "Backup failed" --silent
```

The same two values, given by name. Typing at a prompt, the positional form is
shorter and reads better; assembling a command from variables, the named form
is the one that cannot go wrong — `--body "$out"` sends what `$out` holds even
when that starts with a dash or comes out empty, where a positional would be
read as a flag or shift the arguments along.

A flag wins over the arguments, and takes their meaning with it: with `--title`
given there is no positional title left, so everything remaining is body. An
argument the flags have left with nothing to be is an error rather than a
message quietly missing the part you wrote.

`--body ""` is a body you chose to leave empty, and is not the same as omitting
it: stdin is consulted only when nothing named a body at all.

## Logs and long output

```sh
journalctl -u nightly -n 40 --no-pager | morse send --code "Nightly failed"
journalctl -u nightly -n 40 --no-pager | morse send --code --collapse "Nightly failed"
```

`--code` shows the body as a code block, in monospace, so a log keeps its
columns and a stack trace its indentation. `--collapse` folds the body into a
quote that shows its first few lines and a "show more", so a long excerpt is
there when you want it without pushing everything else up the chat. They work
on their own or together, on `send` and on `edit`. The title stays plain bold
either way: it is what a lock screen shows.

A rewrite replaces the formatting along with the text, so pass the same flags
every time. A tracked status sent with `--code` and rewritten without it comes
back as plain text.

## Sending a file

```sh
morse send --file coverage.html "Coverage report" "78.4%, up from 71"
morse send --file /var/log/nightly.log --silent "Nightly finished"
morse send --file backup.tar.gz
```

The title and body become the file's caption, so a document arrives explained
rather than as an unlabelled attachment. With neither, the file goes out with no
caption at all — a filename is often the whole message, and an empty caption
would only add an empty bold line above it.

A caption holds 1024 characters against a message's 4096, and morse trims to fit
rather than letting the API reject the delivery: the body gives way first, since
the title is what gets read on a lock screen. Telegram accepts uploads up to
50 MB, and morse checks the size before the first byte goes up the wire, so an
oversized file fails immediately instead of after the whole transfer.

`--silent` delivers without a notification sound. The message still arrives and
stays in the chat — it just does not buzz, so routine facts can be reported
without training the reader to mute the chat, which would cost the messages
that matter.

## One topic per app

When several things report to the same chat — a backup, a CI runner, an agent —
their messages run together. Telegram's answer is topics: a group with topics
turned on has one thread per subject, each with its own unread count and its
own mute, in every Telegram client. Give each app a topic and pass it:

```sh
morse send --topic backup --silent "Nightly backup" "✓ done — 412 files"
morse send --topic ci "CI" "main is red"
```

To set it up:

1. Create a group, turn on **Topics** in its settings, and add the bot as an
   admin so it can post in every topic. Give it the **Manage Topics** right too
   if you want `morse topic create` to work.
2. Point `chat_id` (or `MORSE_CHAT_ID`) at the group. Turning on Topics
   converts a basic group into a supergroup, and the supergroup has a **new**
   id — negative, starting with `-100`. An id read before the switch no longer
   works, so read it afterwards.
3. Create a topic per app. The easiest way is to let morse do it, which also
   prints the id:

   ```sh
   morse topic create ci
   ```

   A topic made by hand works just as well. Its id is the number after the
   group in a link to any message in the topic
   (`https://t.me/c/<group>/<topic>/<message>` — the chat id is `-100<group>`),
   or post anything in it and read it back with `morse receive list --json`,
   which shows each message's `topic`. A bot that was not yet an admin when a
   topic was created never sees that it was, so that topic's id has to come
   from a link.
4. Name the topics in the config, so callers can use names instead of numbers:

   ```yaml
   telegram:
     bot_token: "123456:ABC-DEF..."
     chat_id: -1001234567890
     topics:
       backup: 7
       ci: 12
   ```

   `morse topic create` prints the lines to paste. morse does not edit the
   config itself: the file holds your token and your comments. A bot cannot
   list a chat's topics, which is why the names have to be written down.

`--topic` takes a name from the config, in any case, or a topic id. An unknown
name is refused, with the names the config has, rather than sent to General.

Trying it from a shell without touching the config:

```sh
MORSE_CHAT_ID=-1001234567890 morse send --topic 7 "test" "into the topic"
```

The General topic is the chat itself and takes no `--topic` at all. A topic the
chat does not have is refused rather than delivered somewhere else.

A `--track` label remembers its topic, so a message that was deleted is
replaced in the topic it was in, by `send` and by `edit` alike. A message cannot
move between topics, so `send --track` with a different `--topic` than last time
starts a new message there and points the label at it. Moving the config to
another chat works the same way: each label starts a new message in the new
chat on its next `send --track`, and `edit --track` refuses until it has.

Filtering by app is something the chat does, not morse: a bot never sees the
messages it sent itself, so `morse receive` cannot read your apps' notifications
back, by topic or otherwise.

## Updating a message instead of sending another

```sh
morse send --track nightly --silent "Nightly backup" "idle"
morse send --track nightly --silent "Nightly backup" "● running — started 03:00"
morse send --track nightly --silent "Nightly backup" "✓ done 03:14 — 412 files, 1.2 GB"
```

Something long-running has news repeatedly, and one message per update fills
the chat with a history nobody asked for. A `--track` label stands for one line
in the chat: the first `send` puts it there, and every `send` after rewrites it,
so the same line keeps saying what is true now — you look when you want to know,
rather than being told each time.

The three commands above are identical on purpose. A caller reporting
repeatedly does not have to know whether this is its first report or its
hundredth, which is the case that otherwise breaks: a job restarting mid-run, or
one whose state file was wiped, would have to tell a first send from a later one
to avoid failing on the wrong one.

**An edit never notifies anyone.** Not quietly, not with a badge: Telegram does
not notify on edits at all, and that is the API's behaviour rather than a
setting, which is why `edit` has no `--silent` to pass. Only the first `send`
can make a sound, so send it `--silent` too if even that is more than you want.

`--track <label>` is how the second run finds the message the first one sent.
morse writes the message id down under the label — in `$XDG_STATE_HOME/morse`,
else `~/.local/state/morse` — and looks it up again next time, so a script does
not have to thread a variable through a restart.

If the tracked message is gone — you deleted it, or the chat id changed — the
next `send --track` starts a new one and points the label at it. Otherwise
deleting a single message would silence the job behind it for good, and the
message you are meant to interact with is exactly the one that gets deleted.

`morse edit --track <label>` does the same rewrite but insists the label
already means something, failing if it does not. That is the one to reach for
interactively, where a mistyped label should say so rather than quietly start a
second line; `send --track` is the one for a script.

A tracked `--file` send is a new message every time, since an uploaded document
cannot be rewritten in place; the label follows the newest one.

For a caller that would rather hold the id itself:

```sh
id=$(morse send --json "Backup" "0%" | jq .message_id)
morse edit "$id" "Backup" "done"
```

An explicit id means *that* message, so if it is gone the edit fails rather
than sending a replacement — only a label stands for "the message that reports
this thing" rather than one particular message.

`--json` prints the id of the message that now carries the text, which differs
from the one you named exactly when a gone message was replaced.

Rewriting a message to the text it already has is not an error: Telegram
refuses the call, and morse treats that as the success it is — the chat already
says what it was asked to say.

### What morse can and cannot tell you

The label file records what morse last sent or edited, and when. That is
morse's own note, not a report from Telegram: a bot cannot ask the Bot API what
a message currently says, whether it still exists, or whether anyone read it —
there is no `getMessage`. To see the state, look at the chat.

## Reading the chat back

Everything sent *to* the bot — a reply, a snippet pasted from a phone, a file —
can be listed, searched and pulled down:

```sh
morse receive list                          # newest first, id in the first column
morse receive get 481                       # its text, on stdout
morse receive get 481 --save ~/Downloads    # what it carried, into a folder
```

Choosing one of them is a fuzzy finder's job rather than morse's, so the id
comes first on every line and anything can sit in the middle:

```sh
morse receive list | fzf | cut -d' ' -f1 | xargs morse receive get
```

`--json` prints one object per line — `message_id`, `chat_id`, `date`, `from`,
`text`, `file` — for jq or a picker that wants the fields apart.

### Onto the clipboard

`get` writes the text to stdout and stops there, because a clipboard is not
morse's to reach: which program holds one depends on the display server, and
over ssh the clipboard worth writing to is on the *other* end of the connection.
Both are one pipe away, and the pipe knows things morse cannot guess.

The text comes out exactly as it arrived, with no trailing newline of morse's
own — a newline on the end of a pasted command line is a keystroke that runs it.

```sh
morse receive get 481 | wl-copy                    # wayland
morse receive get 481 | xclip -selection clipboard # x11
morse receive get 481 | pbcopy                     # macos
```

Over ssh, where no program on the far machine can reach your clipboard, the
terminal can — with an OSC 52 escape sequence, which travels back down the
connection the session came in on:

```sh
tgcopy() {
    printf '\033]52;c;%s\a' "$(morse receive get "$1" | base64 -w0)" > /dev/tty
}
tgcopy 481
```

That needs a terminal that implements OSC 52 (kitty, WezTerm, foot, iTerm2,
Alacritty and Windows Terminal do; macOS Terminal.app does not), and inside
tmux it needs `set -g set-clipboard on` — the default, `external`, drops
sequences coming from an application.

### What you can see, and why it is a window

The Bot API has no method that reads a chat's history. The only way in is
`getUpdates`, which holds roughly a day of messages and at most a hundred at a
time — so that is the window, and morse keeps no archive of its own to widen it.

Reading never consumes, and that costs morse something to guarantee. There are
three ways to ask, and two take updates away for good — from every reader of
that bot, not just from morse:

- A **positive offset** confirms everything below it, deleting it server-side.
- A **negative offset** means "the last *n*", and the API says of it: *all
  previous updates will be forgotten*. With a backlog longer than *n*, asking
  this way destroys the oldest.
- **No offset** returns updates "starting with the earliest unconfirmed
  update", and forgets nothing.

morse asks the third way, so nothing you run removes a message from the chat or
from anyone else's view of it. The price is that a backlog longer than the
window is read from the wrong end: the oldest come back and the newest sit
behind them, out of reach without a confirmation morse will not make on your
behalf. When the response fills the requested window, `list` warns on stderr
that newer messages may be hidden; the backlog clears itself within a day.

morse also never sends `allowed_updates`. Telegram remembers the last value any
client gave it and applies it to every later call, so narrowing it to the two
types morse reads would quietly stop callback queries and edits reaching
anything else polling the same bot. morse ignores what it does not understand
instead, which changes nothing for anyone.

That is not the same as the window being identical every time you ask.
**Telegram will not hand the same updates over twice in immediate succession**:
a second `getUpdates` within about half a second comes back with the queue's
oldest update and nothing else, and everything reappears once that passes.
Nothing was consumed — they are just not offered again that fast. `get` knows,
and asks a second time before reporting a message missing; two `list` calls
back to back in one script will show you the short answer, so put a beat
between them.

Three things make that window look emptier than expected:

- **In a group, privacy mode is on by default**, and a bot sees only commands
  and replies to itself. Turn it off via @BotFather → Bot Settings → Group
  Privacy. A one-to-one chat is unaffected.
- **A webhook and polling are exclusive.** With one set, `getUpdates` is refused
  outright, and morse says so.
- **A poll moments after another poll** returns the short window described
  above.

A bot may download at most 20 MB, whatever it was allowed to upload. `--save`
refuses a name that is already taken in the target directory rather than
writing over it — the filename came from whoever sent the file, and two people
pick `report.pdf` independently all the time.

## Asking what morse accepts

```sh
morse capabilities --json
```

A script that has to learn the interface by reading this source is coupled to
it; one that can ask is not. It answers even when the config is missing, because
"what is this and how would I call it" is a fair question to ask of an
installation that is not set up yet.

## Writing a caller

Anything that needs to watch something lives outside morse and calls `morse
send` when it has something to say. That keeps the decision — is this worth
reporting? — with the caller who has the context to make it, and leaves morse
with one job it can do without knowing why:

```sh
if ! out=$(rsync -a "$src" "$dst" 2>&1); then
    morse send --title "Backup failed" --body "$out"
fi
```

## Development

```sh
make test     # unit tests, against a fake Bot API
make vet
```

The command tests in `cli_test.go` run whole commands (flags, config, labels,
the calls made and what is printed) against a fake server, through
`MORSE_API_URL`. CI runs them on every push and pull request, with the race
detector and a gofmt check.

What a fake server cannot know is whether Telegram still accepts the markup
morse writes and still words its refusals the way morse reads them. For that
there is a smoke test against the real API:

```sh
MORSE_SMOKE_CHAT_ID=-1001234567890 make smoke
```

It needs a group set aside for it, with topics turned on and the bot an admin
that may manage topics and delete messages. It sends silently, creates one
topic, and deletes everything it made on the way out. It never runs as part of
`make test` or CI.

## License

MIT — see [LICENSE](LICENSE).
