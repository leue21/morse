package main

import (
	"io"
	"strings"
	"testing"
)

func TestResolveMessage(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		stdin      string
		wantTitle  string
		wantBody   string
		wantErrStr string
		noStdin    bool
	}{{
		name: "body from arguments", args: []string{"Disk", "almost", "full"},
		wantTitle: "Disk", wantBody: "almost full",
	}, {
		name: "body piped in", args: []string{"Build failed: nightly"},
		stdin:     "panic: nil map\nexit status 2\n",
		wantTitle: "Build failed: nightly", wantBody: "panic: nil map\nexit status 2",
	}, {
		// A caller with nothing to add still has something to report: a job
		// that died without logging is exactly the case worth hearing about.
		// Nothing is piped in, which is the terminal case: reading stdin there
		// would block instead of sending.
		name: "title alone still reports", args: []string{"Build failed: nightly"}, noStdin: true,

		wantTitle: "Build failed: nightly", wantBody: "(no details)",
	}, {
		name: "nothing at all", wantErrStr: "nothing to send",
	}, {
		name: "nothing at all, no stdin", noStdin: true, wantErrStr: "nothing to send",
	}, {
		name: "flag after the title", args: []string{"Backup", "--silent"},
		wantErrStr: "must come before the title",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdin io.Reader
			if !tc.noStdin {
				stdin = strings.NewReader(tc.stdin)
			}
			title, body, err := resolveMessage(tc.args, stdin)
			if tc.wantErrStr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrStr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErrStr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if title != tc.wantTitle || body != tc.wantBody {
				t.Errorf("got (%q, %q), want (%q, %q)", title, body, tc.wantTitle, tc.wantBody)
			}
		})
	}
}
