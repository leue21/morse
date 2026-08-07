# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

Go binary is at `/usr/local/go/bin/go`. Use the Makefile:

```bash
make build          # compile binary
make test           # run all tests (-v -count=1)
make vet            # static analysis
make clean          # remove binary + coverage.out
make install        # install binary + systemd user service + config
```

Run a single test:
```bash
/usr/local/go/bin/go test ./plugin -run TestBTCPriceName -v
```

## Architecture

**morse** is a plugin-based Telegram alerting bot. Minimal dependencies (only `gopkg.in/yaml.v3`; everything else is stdlib).

```
main.go              → wires config, plugins, scheduler, notifier; handles signals
config/              → YAML parsing; PluginConf provides generic accessors (GetString, GetFloat, GetDuration, etc.)
plugin/              → Plugin interface (Name, Check, Describe) + implementations (btcprice, toogoodtogo)
scheduler/           → runs each plugin in its own goroutine on a time.Ticker, calls Check() immediately then on interval
notifier/            → Telegram Bot API client (Send + long-poll PollCommands)
internal/testutil/   → FakeAPI HTTP mock for tests
```

### Plugin system

Plugins implement `plugin.Plugin` interface (`Name()`, `Check(ctx) ([]Alert, error)`, `Describe()`). Registration is manual in `buildPlugins()` in main.go — each plugin key in config maps to a constructor call. Config values are accessed via `PluginConf` generic accessors, not per-plugin config structs.

### Data flow

Config load → `buildPlugins()` constructs plugins → `scheduler.Add(plugin, interval)` → `scheduler.Run(ctx)` starts per-plugin goroutines → `Check()` returns `[]Alert` → `notifier.Send()` to Telegram.

### Configuration

Config lives at `~/.config/morse/config.yaml` (override with `-config` flag). Plugin data files (e.g., `tgtg_tokens.json`) are stored alongside the config file (`dataDir` = config file's directory).

## Conventions

- Errors are wrapped with `fmt.Errorf("context: %w", err)` and never fatal in the scheduler (logged and continued)
- Logging via `log/slog`; per-plugin loggers use `slog.With("plugin", name)`
- All HTTP calls use `http.NewRequestWithContext(ctx, ...)` for cancellation
- Each plugin creates its own `http.Client` with appropriate timeouts
- Tests use `testutil.FakeAPI` for HTTP mocking; plugins expose internal URLs/fields for testability
