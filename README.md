# morse

Send a notification to Telegram from the command line.

```sh
morse send "Backup failed" "rsync exit 23"
echo "$logs" | morse send "Unit failed: dewey-worker.service"
morse send --silent "Backup finished" "412 files, 3m21s"
```

morse is a CLI and nothing else — no daemon, no schedule, no watching. Anything
that wants to be told something decides for itself when to speak, and calls
morse to do the speaking: a systemd `OnFailure=` handler, a cron job, an agent,
a person at a prompt.

## Install

```sh
make install
```

Installs the binary under `~/.local/bin` and the `notify@.service` handler. No
step needs root, and there is no service to start.

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

With no body argument the body is read from stdin, so a caller can pipe in a
log excerpt. A title on its own still sends: a unit that died without logging
is exactly the case worth hearing about.

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

## Reporting a failed service

`notify@.service` reports a systemd unit that has given up, with the tail of its
journal as the body. Point a unit at it:

```ini
[Unit]
OnFailure=notify@%n.service
StartLimitIntervalSec=300
StartLimitBurst=5
```

A unit only reaches `failed` — and so only triggers `OnFailure` — once it
exhausts its start limit. The default window is 10 seconds, so a service dying
slowly restarts for ever and tells nobody; the wider window above means five
failures in five minutes counts as broken rather than unlucky.

The handler deliberately does not depend on anything of morse's being running,
because morse is not running: it is a binary that gets executed.

## contrib/diskguard

An example consumer, installed separately because it is not part of morse:

```sh
make install-diskguard
```

A systemd timer that warns before a filesystem fills up. On a host that records
live streams this is the one failure that destroys something unrecoverable — a
stream written to a full disk is lost and cannot be fetched again.

| Variable | Default | Meaning |
|---|---|---|
| `DISKGUARD_PATHS` | `/` | Filesystems to check, space separated |
| `DISKGUARD_MIN_FREE_PERCENT` | `8` | Warn below this share free |
| `DISKGUARD_MIN_FREE_GB` | `120` | Warn below this many GB free |
| `DISKGUARD_COOLDOWN_HOURS` | `6` | Minimum hours between warnings per path |

Both thresholds are checked and either firing is enough: a percentage alone is
useless on a large disk, where 5% is still 100 GB, and an absolute floor alone
is useless on a small one. The timer runs every 15 minutes and the cooldown
decides how often it may speak, so detection speed and alert volume are tuned
separately.

Write your own the same way — decide when there is something to say, and let
morse say it.
