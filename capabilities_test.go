package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"morse/plugin"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCapabilitiesReportsTheSendContract(t *testing.T) {
	path := writeConfig(t, `
telegram:
  bot_token: "tok"
  chat_id: 42
plugins:
  diskspace:
    interval: "15m"
    paths: ["/"]
    min_free_percent: 8.0
`)
	var out bytes.Buffer
	if err := cmdCapabilities(path, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var caps Capabilities
	if err := json.Unmarshal(out.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.Name != "morse" {
		t.Errorf("name = %q", caps.Name)
	}
	if !caps.Delivery.Configured || caps.Delivery.Channel != "telegram" {
		t.Errorf("delivery = %+v, want a configured telegram channel", caps.Delivery)
	}
	// A caller must be able to learn the accepted severities without reading
	// the source; that is the whole point of the command.
	if len(caps.Send.Severities) != 3 || caps.Send.Default != "warning" {
		t.Errorf("send contract = %+v", caps.Send)
	}
	if !caps.Send.BodyStdin {
		t.Error("send contract does not mention that the body can come from stdin")
	}
	if len(caps.Watching) != 1 || caps.Watching[0].Name != "diskspace" {
		t.Fatalf("watching = %+v, want the configured plugin", caps.Watching)
	}
	if caps.Watching[0].Interval != "15m0s" {
		t.Errorf("interval = %q", caps.Watching[0].Interval)
	}
}

// Every severity the command advertises must actually be accepted by send.
// Advertising one that is refused would be worse than not advertising at all.
func TestCapabilitiesAdvertisesOnlyUsableSeverities(t *testing.T) {
	path := writeConfig(t, "telegram:\n  bot_token: \"tok\"\n  chat_id: 42\n")
	var out bytes.Buffer
	if err := cmdCapabilities(path, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var caps Capabilities
	if err := json.Unmarshal(out.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	// The contract is that every advertised name parses.
	for _, name := range caps.Send.Severities {
		if _, err := plugin.ParseSeverity(name); err != nil {
			t.Errorf("advertised severity %q is not accepted: %v", name, err)
		}
	}
}

// An installation that is not set up yet must still be able to say what it is
// and how it would be called — refusing would make the command useless exactly
// when someone is trying to set it up.
func TestCapabilitiesAnswersWithoutAConfig(t *testing.T) {
	var out bytes.Buffer
	if err := cmdCapabilities("/nonexistent/config.yaml", nil, &out); err != nil {
		t.Fatalf("capabilities refused without a config: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "not loaded") {
		t.Errorf("output does not explain the missing config: %q", text)
	}
	if !strings.Contains(text, "morse send") {
		t.Error("output omits the send contract, which does not depend on config")
	}
	if !strings.Contains(text, "not configured") {
		t.Error("output does not report that delivery is unconfigured")
	}
}
