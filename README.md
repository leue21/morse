# morse

Send a notification to Telegram from the command line.

```sh
morse send "Backup failed" "rsync exit 23"
echo "$logs" | morse send "Build failed on main"
morse send --silent "Backup finished" "412 files, 3m21s"
morse send --title "Backup failed" --body "$out"
morse send --file report.pdf "Nightly report"
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
morse capabilities [--json]                          what morse accepts
morse version
morse help
```

`send` and `capabilities` take `--config <path>` to read credentials from
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
