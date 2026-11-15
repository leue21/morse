package notifier

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/leue21/morse/internal/testutil"
)

func TestCaptionFormatsTheBodyAlone(t *testing.T) {
	tests := []struct {
		name        string
		title, body string
		format      Format
		want        string
	}{{
		name: "code", title: "Build failed", body: "panic: nil map\n\tmain.go:12",
		format: Format{Code: true},
		want:   "<b>Build failed</b>\n<pre>panic: nil map\n\tmain.go:12</pre>",
	}, {
		name: "collapse", title: "Unit failed", body: "line 1\nline 2",
		format: Format{Collapse: true},
		want:   "<b>Unit failed</b>\n<blockquote expandable>line 1\nline 2</blockquote>",
	}, {
		// The quote is what folds and the block is what it holds; Telegram
		// accepts it this way round.
		name: "both nest the block inside the quote", title: "T", body: "x",
		format: Format{Code: true, Collapse: true},
		want:   "<b>T</b>\n<blockquote expandable><pre>x</pre></blockquote>",
	}, {
		name: "a body without a title is still formatted", body: "x",
		format: Format{Code: true},
		want:   "<pre>x</pre>",
	}, {
		// Nothing to format means no empty block under the title.
		name: "a title alone stays a title", title: "T",
		format: Format{Code: true, Collapse: true},
		want:   "<b>T</b>",
	}, {
		// Inside a block the text is still escaped: a < in a log line is not a
		// tag, and Telegram would reject the message if it read it as one.
		name: "code is still escaped", body: "if a < b && c > d",
		format: Format{Code: true},
		want:   "<pre>if a &lt; b &amp;&amp; c &gt; d</pre>",
	}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := caption(tc.title, tc.body, 100, tc.format); got != tc.want {
				t.Errorf("caption = %q, want %q", got, tc.want)
			}
		})
	}
}

// The markup is not text Telegram counts, so a body cut to fit stays whole
// markup: the closing tag has to survive the cut.
func TestCaptionClosesTheBlockAfterACut(t *testing.T) {
	got := caption("T", strings.Repeat("x", 500), 100, Format{Code: true, Collapse: true})
	if !strings.HasSuffix(got, "…</pre></blockquote>") {
		t.Errorf("caption = %q, want the cut inside a closed block", got)
	}
}

// A rewrite replaces the markup along with the text, so an edit has to carry
// the format or a block sent as code comes back as prose.
func TestEditCarriesTheFormat(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/editMessageText", 200, `{"ok":true,"result":{"message_id":7}}`).
		Start()
	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	if err := tg.Edit(context.Background(), 7, "Build", "exit 2", Format{Code: true}); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(srv.Requests[0].Body, &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["text"]; got != "<b>Build</b>\n<pre>exit 2</pre>" {
		t.Errorf("text = %q", got)
	}
}

func TestSendCarriesTheFormat(t *testing.T) {
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendMessage", 200, `{"ok":true}`).
		Start()
	tg := NewTelegram("tok", 42)
	tg.baseURL = srv.URL()

	if _, err := tg.Send(context.Background(), "Build", "exit 2", SendOptions{Format: Format{Collapse: true}}); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(srv.Requests[0].Body, &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["text"]; got != "<b>Build</b>\n<blockquote expandable>exit 2</blockquote>" {
		t.Errorf("text = %q", got)
	}
}
