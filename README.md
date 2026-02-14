# salert

Plugin-based Telegram notification service. Monitors external APIs on a schedule and sends Telegram messages when conditions are met. Designed for Raspberry Pi (ARM64) — minimal dependencies, low resource usage.

## Quick Start

```bash
# Build
make build

# Install as system service (requires sudo)
sudo make install

# Edit config with your Telegram credentials
sudo vim /etc/salert/config.yaml

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
  btcprice:
    interval: "1m"          # poll frequency
    above_usd: 100000.0     # alert when BTC >= this
    below_usd: 50000.0      # alert when BTC <= this
    cooldown: "30m"          # suppress repeat alerts

  toogoodtogo:
    interval: "5m"
    email: "user@example.com"
    store_ids: ["abc123"]
```

### Getting Telegram Credentials

1. Message [@BotFather](https://t.me/BotFather) to create a bot and get the `bot_token`
2. Send a message to your bot, then fetch `https://api.telegram.org/bot<TOKEN>/getUpdates` to find your `chat_id`

## Included Plugins

### btcprice

Monitors Bitcoin price via the CoinGecko free API. Alerts when the price crosses above or below configured thresholds. Supports a cooldown to prevent alert floods.

| Config Key | Type | Description |
|---|---|---|
| `interval` | duration | Poll frequency (default: `5m`) |
| `above_usd` | float | Alert when price >= value (0 to disable) |
| `below_usd` | float | Alert when price <= value (0 to disable) |
| `cooldown` | duration | Min time between alerts (0 to disable) |

### toogoodtogo

Skeleton plugin for TooGoodToGo surprise bag monitoring. The TGTG API requires a complex email-based OTP auth flow that is not yet implemented — the structure is in place for future development.

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
sudo make install       # install binary, config, service file, create salert user
```

This installs:
- `/usr/local/bin/salert` — binary
- `/etc/salert/config.yaml` — config (not overwritten on reinstall)
- `/etc/systemd/system/salert.service` — systemd unit
- `salert` system user (no login shell)

### Uninstall

```bash
sudo make uninstall     # stop, disable, remove binary + service file
```

Config is preserved at `/etc/salert/` — remove manually if no longer needed.

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
sudo systemctl status salert
journalctl -u salert -f
journalctl -u salert --since "1 hour ago"
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
├── salert.service       # systemd unit file
├── config.yaml          # Example configuration
├── config/
│   └── config.go        # YAML config structs + loader
├── notifier/
│   └── telegram.go      # Telegram Bot API client
├── plugin/
│   ├── plugin.go        # Plugin interface + Alert struct
│   ├── btcprice.go      # BTC price monitoring
│   └── toogoodtogo.go   # TooGoodToGo (skeleton)
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
- Dedicated `salert` system user (no login shell)
- `NoNewPrivileges=true` — cannot gain additional privileges
- `ProtectSystem=strict` — filesystem is read-only except explicit paths
- `ProtectHome=true` — no access to home directories
- Config file is `0640 root:salert` — only root can edit, service can read

## License

MIT
