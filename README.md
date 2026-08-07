# morse

Plugin-based Telegram notification service. Monitors external APIs on a schedule and sends Telegram messages when conditions are met. Designed for Raspberry Pi (ARM64) — minimal dependencies, low resource usage.

## Quick Start

```bash
# Build
make build

# Install the binary, the unit and an example config
sudo make install

# Edit config with your Telegram credentials
sudo vim /etc/morse/config.yaml

# Enable and start
sudo make enable
```

## Configuration

Create a `config.yaml` (see included example):

```yaml
telegram:
  bot_token: "123456:ABC-DEF..."   # from @BotFather
  chat_id: 12345678                # your chat/group ID

plugins:
  diskspace:
    interval: "15m"
    paths: ["/"]
    min_free_percent: 8.0
    min_free_gb: 120.0
    cooldown: "6h"
```

### Getting Telegram Credentials

1. Message [@BotFather](https://t.me/BotFather) to create a bot and get the `bot_token`
2. Send a message to your bot, then fetch `https://api.telegram.org/bot<TOKEN>/getUpdates` to find your `chat_id`

## Included Plugins

### diskspace

Warns before a filesystem fills up. On a host that records live streams this is
the only failure that destroys something unrecoverable: a stream written to a
full disk is lost and cannot be fetched again.

Both thresholds are checked and either firing is enough, because either alone is
useless at one end of the range — 5% of a 2 TB disk is still 100 GB, and 40% of
a 20 GB one is not enough for a long recording. A path that cannot be read
alerts as well: an unmounted fileserver otherwise looks exactly like an empty
one.

| Config Key | Type | Description |
|---|---|---|
| `interval` | duration | Poll frequency (default: `5m`) |
| `paths` | list | Filesystems to check; one path per mount point |
| `min_free_percent` | float | Alert below this share free (0 to disable) |
| `min_free_gb` | float | Alert below this many GB free (0 to disable) |
| `cooldown` | duration | Min time between alerts per path (0 to disable) |

Its alerts are `warning`: a filling disk wants attention today, not this second.

## Asking what morse can do

```sh
morse capabilities          # human readable
morse capabilities --json   # for a caller
```

Reports the revision it was built from, whether delivery is configured, the
exact `send` usage with the severities it accepts, and what is currently being
watched with each plugin's present reading. A service that has to learn morse's
interface by reading this source is coupled to it; one that can ask is not.

It answers even when the config is missing or invalid — "what is this and how
would I call it" is a fair question to ask of an installation that is not set
up yet.

## Reporting a failed service

`morse send <title> [body]` posts a single message and exits. With no body it
reads stdin, so a systemd `OnFailure=` handler can pipe in the failed unit's
journal.

`--severity` says how much the reader should care. `info` arrives silently —
the message is in the chat but does not buzz — while `warning` (the default)
and `critical` notify. Routine facts should be `info`: a monitor that
interrupts you for everything gets muted, which costs the alerts that matter.

```ini
[Service]
Type=oneshot
ExecStart=/bin/sh -c 'journalctl --user-unit=%i -n 15 --no-pager --output=cat | %h/.local/bin/morse send --severity critical "Unit failed: %i"'
```

`notify@.service` in this repository does exactly that. Point a unit at it with
`OnFailure=notify@%n.service`. It deliberately does not depend on
`morse.service`, so it still delivers when the daemon itself is what died.

Note that a unit only reaches `failed` — and so only triggers `OnFailure` —
once it exhausts its start limit. The default window is 10 seconds, so a
service dying slowly restarts for ever and tells nobody; set
`StartLimitIntervalSec` wide enough that repeated failure means broken.

## Writing a New Plugin

1. Create `plugin/myplugin.go` implementing the `Plugin` interface:

```go
package plugin

import "context"

type MyPlugin struct {
    // config fields, state
}

func NewMyPlugin(/* config params */) *MyPlugin {
    return &MyPlugin{/* ... */}
}

func (p *MyPlugin) Name() string { return "myplugin" }

func (p *MyPlugin) Check(ctx context.Context) ([]Alert, error) {
    // Call your API, check conditions, return alerts.
    // Return nil, nil if nothing to report.
    // Errors are logged and retried next interval.
    return nil, nil
}
```

2. Add config fields to `config/config.go` if needed (the generic `PluginConf` accessors — `GetString`, `GetFloat`, `GetDuration`, `GetStringSlice` — handle most cases without extra structs).

3. Wire it up in `main.go`'s `buildPlugins()`:

```go
if pc, ok := cfg.Plugins["myplugin"]; ok {
    p := plugin.NewMyPlugin(pc.GetString("api_key"))
    sched.Add(p, pc.ParseInterval())
}
```

4. Add configuration to your `config.yaml`:

```yaml
plugins:
  myplugin:
    interval: "10m"
    api_key: "..."
```

## Installation

### Install

```bash
make build              # compile binary
sudo make install       # install binary, config, service file, create morse user
```

This installs:
- `/usr/local/bin/morse` — binary
- `/etc/morse/config.yaml` — config (not overwritten on reinstall)
- `/etc/systemd/system/morse.service` — systemd unit
- `morse` system user (no login shell)

### Uninstall

```bash
sudo make uninstall     # stop, disable, remove binary + service file
```

Config is preserved at `/etc/morse/` — remove manually if no longer needed.

### Upgrade

```bash
git pull
sudo make install       # rebuilds and replaces binary, preserves config
sudo make restart
```

## Service Management

```bash
sudo make enable        # enable + start
sudo make disable       # disable + stop
sudo make restart       # restart after config change
make status             # show service status
make logs               # follow journal logs (ctrl-c to stop)
```

Or use systemctl/journalctl directly:

```bash
sudo systemctl status morse
journalctl -u morse -f
journalctl -u morse --since "1 hour ago"
```

## Makefile Targets

| Target | Description |
|---|---|
| `make build` | Compile binary |
| `make test` | Run all tests |
| `make vet` | Run go vet |
| `make clean` | Remove build artifacts |
| `sudo make install` | Install binary, config, service file |
| `sudo make uninstall` | Remove binary and service file |
| `sudo make enable` | Enable and start service |
| `sudo make disable` | Disable and stop service |
| `sudo make restart` | Restart service |
| `make status` | Show service status |
| `make logs` | Follow journal logs |

## Project Structure

```
├── main.go              # Entry point, plugin wiring, signal handling
├── Makefile             # Build, install, service management
├── morse.service       # systemd unit file
├── config.yaml          # Example configuration
├── config/
│   └── config.go        # YAML config structs + loader
├── notifier/
│   └── telegram.go      # Telegram Bot API client
├── plugin/
│   ├── plugin.go        # Plugin interface + Alert struct
│   └── diskspace.go     # free-space monitoring
└── scheduler/
    └── scheduler.go     # Per-plugin goroutine scheduler
```

## Testing

```bash
make test                                                       # run all tests
go test ./... -cover                                            # with coverage summary
go test ./... -coverprofile=c.out && go tool cover -html=c.out  # HTML report
```

## Dependencies

- `gopkg.in/yaml.v3` — YAML config parsing
- Everything else is Go stdlib (`net/http`, `encoding/json`, `log/slog`, `time`, `context`)

## Security

The systemd service runs with hardened settings:
- Dedicated `morse` system user (no login shell)
- `NoNewPrivileges=true` — cannot gain additional privileges
- `ProtectSystem=strict` — filesystem is read-only except explicit paths
- `ProtectHome=true` — no access to home directories
- Config file is `0640 root:morse` — only root can edit, service can read

## License

MIT
