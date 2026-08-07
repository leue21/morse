# morse

Send a notification to Telegram from the command line.

```sh
morse send "Backup failed" "rsync exit 23"
echo "$logs" | morse send "Build failed on main"
morse send --silent "Backup finished" "412 files, 3m21s"
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
morse send [--silent] <title> [body]   send a message
morse capabilities [--json]            what morse accepts, for a caller
morse help
```

Both subcommands take `--config <path>` to read credentials from somewhere
other than `~/.config/morse/config.yaml`. Flags come before the title; a flag
after it would otherwise end up in the message body.

With no body argument the body is read from stdin when something is piped in,
so a caller can pass a log excerpt that way. A title on its own still sends: a
job that died without logging is exactly the case worth hearing about.

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
    morse send "Backup failed" "$out"
fi
```

## License

MIT — see [LICENSE](LICENSE).
