package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"morse/config"
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
	path := writeConfig(t, "telegram:\n  bot_token: \"tok\"\n  chat_id: 42\n")

	var out bytes.Buffer
	if err := cmdCapabilities(path, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var caps Capabilities
	if err := json.Unmarshal(out.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.Name != "morse" || !caps.Delivery.Configured {
		t.Errorf("got %+v", caps)
	}
	if !strings.Contains(caps.Send.Usage, "--silent") {
		t.Errorf("usage does not mention --silent: %q", caps.Send.Usage)
	}
	if !caps.Send.BodyStdin {
		t.Error("contract omits that the body can come from stdin")
	}
	// The advertised variables must be the ones config actually reads, or a
	// caller sets something that does nothing.
	want := []string{config.BotTokenEnv, config.ChatIDEnv}
	if len(caps.Delivery.Env) != len(want) {
		t.Fatalf("env = %v, want %v", caps.Delivery.Env, want)
	}
	for i, name := range want {
		if caps.Delivery.Env[i] != name {
			t.Errorf("env[%d] = %q, want %q", i, caps.Delivery.Env[i], name)
		}
	}
}

// An installation that is not set up yet must still say what it is and how it
// would be called; that is when someone most needs to know.
func TestCapabilitiesAnswersWithoutAConfig(t *testing.T) {
	t.Setenv(config.BotTokenEnv, "")
	t.Setenv(config.ChatIDEnv, "")

	var out bytes.Buffer
	if err := cmdCapabilities("/nonexistent/config.yaml", nil, &out); err != nil {
		t.Fatalf("capabilities refused without a config: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "not configured") {
		t.Errorf("output does not report unconfigured delivery: %q", text)
	}
	if !strings.Contains(text, "morse send") {
		t.Error("output omits the send contract, which does not depend on config")
	}
}
