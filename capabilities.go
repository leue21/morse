package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"morse/config"
)

// Capabilities is what morse accepts, in a form a caller can read without
// importing anything or reading this source. A script that has to learn the
// interface by reading the code is coupled to it; one that can ask is not.
type Capabilities struct {
	Name      string       `json:"name"`
	Version   string       `json:"version"`
	Config    string       `json:"config"`
	ConfigErr string       `json:"config_error,omitempty"`
	Delivery  Delivery     `json:"delivery"`
	Send      SendContract `json:"send"`
}

type Delivery struct {
	Channel    string `json:"channel"`
	Configured bool   `json:"configured"`
	// Env names the variables that supply credentials without a config file.
	Env []string `json:"env"`
}

// SendContract describes `morse send` precisely enough to call it blind.
type SendContract struct {
	Usage     string `json:"usage"`
	Silent    string `json:"silent"`
	BodyStdin bool   `json:"body_from_stdin"`
	File      string `json:"file"`
}

// cmdCapabilities reports the interface. It answers even when the config is
// missing or invalid: "what is this and how would I call it" is a fair question
// to ask of an installation that is not set up yet, and refusing would make the
// command useless exactly when it is most wanted.
func cmdCapabilities(defaultConfig string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("capabilities", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the error is returned and reported once, by main
	configPath := fs.String("config", defaultConfig, "path to config file")
	asJSON := fs.Bool("json", false, "emit the capabilities as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	caps := Capabilities{
		Name:    "morse",
		Version: buildVersion(),
		Config:  *configPath,
		Delivery: Delivery{
			Channel: "telegram",
			Env:     []string{config.BotTokenEnv, config.ChatIDEnv},
		},
		Send: SendContract{
			Usage:     "morse send [--silent] [--file path] <title> [body]",
			Silent:    "--silent delivers without a notification sound; the message still arrives",
			BodyStdin: true,
			File:      "--file uploads that file, with the title and body as its caption; at most 50 MB",
		},
	}
	if _, err := config.Load(*configPath); err != nil {
		caps.ConfigErr = err.Error()
	} else {
		caps.Delivery.Configured = true
	}

	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(caps)
	}

	state := "not configured"
	if caps.Delivery.Configured {
		state = "configured"
	}
	fmt.Fprintf(out, "%s %s\nconfig    %s\ndelivery  %s (%s)\n",
		caps.Name, caps.Version, caps.Config, caps.Delivery.Channel, state)
	if caps.ConfigErr != "" {
		fmt.Fprintf(out, "          %s\n", caps.ConfigErr)
	}
	fmt.Fprintf(out, "env       %s\n\nsend\n  %s\n  %s\n  %s\n  the body is read from stdin when no body argument is given\n",
		strings.Join(caps.Delivery.Env, " "), caps.Send.Usage, caps.Send.Silent, caps.Send.File)
	return nil
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
	return revision[:min(len(revision), 12)] + modified
}
