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
		haveFile   bool
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
	}, {
		// The file is the delivery; a caption reading "(no details)" would say
		// less about it than saying nothing at all.
		name: "a file alone is enough to send", haveFile: true, noStdin: true,
		wantTitle: "", wantBody: "",
	}, {
		name: "a titled file keeps its title", args: []string{"Nightly report"},
		haveFile: true, noStdin: true,
		wantTitle: "Nightly report", wantBody: "",
	}, {
		name: "a file still takes a piped body", args: []string{"Nightly report"},
		haveFile: true, stdin: "412 files, 3m21s\n",
		wantTitle: "Nightly report", wantBody: "412 files, 3m21s",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdin io.Reader
			if !tc.noStdin {
				stdin = strings.NewReader(tc.stdin)
			}
			title, body, err := resolveMessage(tc.args, stdin, tc.haveFile)
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
