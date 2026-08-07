package main

import (
	"io"
	"strings"
	"testing"
)

// ptr marks a flag as passed: resolveMessage tells --body "" from no --body at
// all, and a test table needs to say which it means.
func ptr(s string) *string { return &s }

func TestResolveMessage(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		titleFlag  *string
		bodyFlag   *string
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
	}, {
		name: "named flags", titleFlag: ptr("Disk"), bodyFlag: ptr("almost full"),
		noStdin:   true,
		wantTitle: "Disk", wantBody: "almost full",
	}, {
		// A value that starts with a dash is the case the positional form
		// cannot take at all.
		name: "a body may start with a dash", titleFlag: ptr("Diff"),
		bodyFlag: ptr("-3 lines"), noStdin: true,
		wantTitle: "Diff", wantBody: "-3 lines",
	}, {
		// With the title named, nothing is left for the arguments to be but
		// the body — including a first word that would otherwise be the title.
		name: "arguments are the body once the title is named",
		args: []string{"almost", "full"}, titleFlag: ptr("Disk"), noStdin: true,
		wantTitle: "Disk", wantBody: "almost full",
	}, {
		name: "a named body still leaves the title positional",
		args: []string{"Disk"}, bodyFlag: ptr("almost full"), noStdin: true,
		wantTitle: "Disk", wantBody: "almost full",
	}, {
		// Dropping it would send a message missing the part the caller wrote.
		name: "an argument with nothing left to be", args: []string{"Disk", "full"},
		titleFlag: ptr("Disk"), bodyFlag: ptr("almost full"), noStdin: true,
		wantErrStr: "has nothing to be",
	}, {
		// The flag was passed, so the caller has answered the question that
		// stdin would otherwise be asked.
		name: "an explicit empty body does not fall through to stdin",
		titleFlag: ptr("Build failed"), bodyFlag: ptr(""),
		stdin:     "panic: nil map\n",
		wantTitle: "Build failed", wantBody: "(no details)",
	}, {
		name: "a named title still takes a piped body", titleFlag: ptr("Build failed"),
		stdin:     "panic: nil map\n",
		wantTitle: "Build failed", wantBody: "panic: nil map",
	}, {
		name: "named nothing", titleFlag: ptr(""), bodyFlag: ptr(""), noStdin: true,
		wantErrStr: "nothing to send",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdin io.Reader
			if !tc.noStdin {
				stdin = strings.NewReader(tc.stdin)
			}
			title, body, err := resolveMessage(tc.args, stdin, tc.haveFile, tc.titleFlag, tc.bodyFlag)
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
