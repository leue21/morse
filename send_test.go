package main

import (
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
	}{{
		name: "body from arguments", args: []string{"Disk", "almost", "full"},
		wantTitle: "Disk", wantBody: "almost full",
	}, {
		name: "body piped in", args: []string{"Unit failed: dewey.service"},
		stdin:     "panic: nil map\nexit status 2\n",
		wantTitle: "Unit failed: dewey.service", wantBody: "panic: nil map\nexit status 2",
	}, {
		// A caller with nothing to add still has something to report: a unit
		// that died without logging is exactly the case worth hearing about.
		name: "title alone still reports", args: []string{"Unit failed: dewey.service"},
		wantTitle: "Unit failed: dewey.service", wantBody: "(no details)",
	}, {
		name: "nothing at all", wantErrStr: "nothing to send",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			title, body, err := resolveMessage(tc.args, strings.NewReader(tc.stdin))
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
