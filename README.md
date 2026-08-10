# morse

Send a notification to Telegram from the command line.

```sh
morse send "Backup failed" "rsync exit 23"
echo "$logs" | morse send "Build failed on main"
morse send --silent "Backup finished" "412 files, 3m21s"
morse send --title "Backup failed" --body "$out"
morse send --file report.pdf "Nightly report"
morse send --track nightly "Nightly backup" "✓ done — 412 files"
```

morse sends one message and exits. Anything that wants to be told something
decides for itself when to speak, and calls morse to do the speaking: a script,
a cron job, an agent, a person at a prompt.

## Install

```sh
make install
```

Installs the binary under `~/.local/bin` and — only if there is not one already
— an example `~/.config/morse/config.yaml` to fill in. No step needs root.

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

### Getting Telegram credentials

1. Message [@BotFather](https://t.me/BotFather) to create a bot and get the `bot_token`
2. Send a message to your bot, then fetch `https://api.telegram.org/bot<TOKEN>/getUpdates` to find your `chat_id`

## Usage

```
morse send [--silent] [--file path] <title> [body]   send a message
morse send --title <t> --body <b>                    the same, named
morse send --track <label> [--json] <title> [body]   send, and remember it
morse edit <message_id> <title> [body]               rewrite a sent message
morse edit --track <label> [--json] <title> [body]   the same, by label
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

## License

MIT — see [LICENSE](LICENSE).
