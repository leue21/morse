package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"morse/config"
	"morse/plugin"
	"morse/scheduler"
)

// Capabilities is what morse can be asked to do, in a form another service can
// read without importing anything or reading this source. A sender that has to
// know morse's interface by reading its code is coupled to it; one that can ask
// is not.
type Capabilities struct {
	Name      string        `json:"name"`
	Version   string        `json:"version"`
	Config    string        `json:"config"`
	ConfigErr string        `json:"config_error,omitempty"`
	Delivery  Delivery      `json:"delivery"`
	Send      SendContract  `json:"send"`
	Watching  []WatchedItem `json:"watching"`
}

type Delivery struct {
	Channel    string `json:"channel"`
	Configured bool   `json:"configured"`
}

// SendContract describes `morse send` precisely enough to call it blind.
type SendContract struct {
	Usage      string   `json:"usage"`
	Severities []string `json:"severities"`
	Default    string   `json:"default_severity"`
	BodyStdin  bool     `json:"body_from_stdin"`
}

type WatchedItem struct {
	Name     string `json:"name"`
	Interval string `json:"interval"`
	State    string `json:"state,omitempty"`
}

// cmdCapabilities reports the interface. It deliberately still answers when the
// config is missing or invalid: "what can this do and how do I call it" is a
// fair question to ask of an installation that is not set up yet, and refusing
// would make the command useless exactly when it is most wanted.
func cmdCapabilities(defaultConfig string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("capabilities", flag.ExitOnError)
	configPath := fs.String("config", defaultConfig, "path to config file")
	asJSON := fs.Bool("json", false, "emit the capabilities as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	caps := Capabilities{
		Name:     "morse",
		Version:  buildVersion(),
		Config:   *configPath,
		Delivery: Delivery{Channel: "telegram"},
		Send: SendContract{
			Usage:      "morse send [--severity info|warning|critical] <title> [body]",
			Severities: []string{string(plugin.SeverityInfo), string(plugin.SeverityWarning), string(plugin.SeverityCritical)},
			Default:    string(plugin.SeverityWarning),
			BodyStdin:  true,
		},
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		caps.ConfigErr = err.Error()
	} else {
		caps.Delivery.Configured = cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != 0
		// Building against a scheduler with no notifier is safe: nothing is
		// run here, the entries are only read back for their intervals.
		sched := scheduler.New(nil)
		buildPlugins(cfg, sched)
		for _, e := range sched.Entries() {
			caps.Watching = append(caps.Watching, WatchedItem{
				Name:     e.Plugin.Name(),
				Interval: e.Interval.String(),
				State:    strings.TrimRight(e.Plugin.Describe(), "\n"),
			})
		}
	}

	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(caps)
	}
	writeCapabilities(out, caps)
	return nil
}

func writeCapabilities(out io.Writer, caps Capabilities) {
	fmt.Fprintf(out, "%s %s\n", caps.Name, caps.Version)
	fmt.Fprintf(out, "config    %s\n", caps.Config)
	if caps.ConfigErr != "" {
		fmt.Fprintf(out, "          not loaded: %s\n", caps.ConfigErr)
	}
	state := "not configured"
	if caps.Delivery.Configured {
		state = "configured"
	}
	fmt.Fprintf(out, "delivery  %s (%s)\n", caps.Delivery.Channel, state)

	fmt.Fprintf(out, "\nsend\n  %s\n", caps.Send.Usage)
	fmt.Fprintf(out, "  severities: %s (default %s)\n",
		strings.Join(caps.Send.Severities, ", "), caps.Send.Default)
	if caps.Send.BodyStdin {
		fmt.Fprintf(out, "  the body is read from stdin when no body argument is given\n")
	}

	if len(caps.Watching) == 0 {
		fmt.Fprintf(out, "\nwatching\n  nothing\n")
		return
	}
	fmt.Fprintf(out, "\nwatching\n")
	for _, w := range caps.Watching {
		fmt.Fprintf(out, "  %s (every %s)\n", w.Name, w.Interval)
		for _, line := range strings.Split(w.State, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			fmt.Fprintf(out, "  %s\n", strings.TrimRight(line, " "))
		}
	}
}

// buildVersion reports the revision this binary was built from. Go records it
// automatically for a build from a git checkout, so nothing has to be stamped
// by hand or kept in step with a release.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			if setting.Value == "true" {
				modified = "-dirty"
			}
		}
	}
	if revision == "" {
		return "devel"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	return revision + modified
}
