package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/leue21/morse/internal/testutil"
	"github.com/leue21/morse/track"
)

// These run whole commands — flags, config, labels, the API calls and what is
// printed — against a fake Bot API, through the same api_url setting a
// self-hosted server uses. They cover the decisions that matter most and that
// no single package sees on its own: whether a tracked send edits or sends,
// what happens to a refused edit, and where a replacement goes.

// fake is a Bot API that hands out message ids and lets a test decide how an
// edit is answered.
type fake struct {
	*testutil.FakeAPI
	mu     sync.Mutex
	nextID int64
	// editRefusal, when set, is the description an edit is refused with.
	editRefusal string
	// pinRefusal, when set, is the description a pin is refused with.
	pinRefusal string
}

// newFake starts a fake API and points every command in the test at it, with
// labels kept in a directory of the test's own.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{nextID: 100}
	f.FakeAPI = testutil.NewFakeAPI(t).
		HandleFunc("POST", "/bottok/sendMessage", f.newMessage).
		HandleFunc("POST", "/bottok/sendDocument", f.newMessage).
		HandleFunc("POST", "/bottok/sendPhoto", f.newMessage).
		HandleFunc("POST", "/bottok/pinChatMessage", func(w http.ResponseWriter, _ *http.Request) {
			f.mu.Lock()
			refusal := f.pinRefusal
			f.mu.Unlock()
			if refusal != "" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `{"ok":false,"error_code":400,"description":%q}`, "Bad Request: "+refusal)
				return
			}
			fmt.Fprint(w, `{"ok":true,"result":true}`)
		}).
		Handle("POST", "/bottok/deleteMessages", 200, `{"ok":true,"result":true}`).
		HandleFunc("POST", "/bottok/editMessageText", func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			refusal := f.editRefusal
			f.mu.Unlock()
			if refusal != "" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `{"ok":false,"error_code":400,"description":%q}`, "Bad Request: "+refusal)
				return
			}
			var req struct {
				MessageID int64 `json:"message_id"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d}}`, req.MessageID)
		}).
		Start()
	t.Setenv("MORSE_BOT_TOKEN", "tok")
	t.Setenv("MORSE_CHAT_ID", "42")
	t.Setenv("MORSE_API_URL", f.URL())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return f
}

func (f *fake) newMessage(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.nextID++
	id := f.nextID
	f.mu.Unlock()
	fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d}}`, id)
}

func (f *fake) refuseEdits(description string) {
	f.mu.Lock()
	f.editRefusal = description
	f.mu.Unlock()
}

// calls lists the methods called, in order.
func (f *fake) calls() []string {
	var methods []string
	for _, r := range f.Requests {
		methods = append(methods, filepath.Base(r.Path))
	}
	return methods
}

// payload decodes the JSON body of the i-th request.
func (f *fake) payload(t *testing.T, i int) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(f.Requests[i].Body, &p); err != nil {
		t.Fatalf("request %d: %v", i, err)
	}
	return p
}

// noConfig is a config path that does not exist, so the environment is all
// there is.
func noConfig(t *testing.T) string {
	return filepath.Join(t.TempDir(), "absent.yaml")
}

// sentID runs a command with --json and reads back the message id it printed.
func sentID(t *testing.T, cmd func(context.Context, string, []string, *strings.Builder) error, args ...string) int64 {
	t.Helper()
	var out strings.Builder
	if err := cmd(context.Background(), noConfig(t), append([]string{"--json"}, args...), &out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	return got.MessageID
}

func send(ctx context.Context, cfg string, args []string, out *strings.Builder) error {
	return cmdSend(ctx, cfg, args, out)
}

func edit(ctx context.Context, cfg string, args []string, out *strings.Builder) error {
	return cmdEdit(ctx, cfg, args, out)
}

func same(got []string, want ...string) bool {
	return strings.Join(got, " ") == strings.Join(want, " ")
}

func TestSendDeliversOneMessage(t *testing.T) {
	f := newFake(t)
	id := sentID(t, send, "--silent", "Backup", "done")
	if id != 101 {
		t.Errorf("id = %d, want 101", id)
	}
	if !same(f.calls(), "sendMessage") {
		t.Fatalf("calls = %v", f.calls())
	}
	p := f.payload(t, 0)
	if p["text"] != "<b>Backup</b>\ndone" || p["disable_notification"] != true || p["chat_id"] != float64(42) {
		t.Errorf("payload = %v", p)
	}
}

// The point of --track: the same call every time, sent once and rewritten
// after, with the label carrying the id between runs.
func TestTrackedSendRewritesAfterTheFirst(t *testing.T) {
	f := newFake(t)
	first := sentID(t, send, "--track", "nightly", "Nightly", "running")
	second := sentID(t, send, "--track", "nightly", "Nightly", "done")
	if first != second {
		t.Errorf("ids %d then %d, want the same message", first, second)
	}
	if !same(f.calls(), "sendMessage", "editMessageText") {
		t.Fatalf("calls = %v", f.calls())
	}
	if p := f.payload(t, 1); p["message_id"] != float64(first) {
		t.Errorf("edited %v, want %d", p["message_id"], first)
	}
}

// Telegram refuses an edit that changes nothing; reporting an unchanged state
// is the caller doing its job, not a failure.
func TestTrackedSendOfUnchangedTextSucceeds(t *testing.T) {
	f := newFake(t)
	first := sentID(t, send, "--track", "nightly", "Nightly", "idle")
	f.refuseEdits("message is not modified: specified new message content and reply markup are exactly the same")
	if again := sentID(t, send, "--track", "nightly", "Nightly", "idle"); again != first {
		t.Errorf("id = %d, want %d", again, first)
	}
	if !same(f.calls(), "sendMessage", "editMessageText") {
		t.Errorf("calls = %v, want no second send", f.calls())
	}
}

func TestTrackedSendReplacesADeletedMessage(t *testing.T) {
	f := newFake(t)
	first := sentID(t, send, "--track", "nightly", "Nightly", "running")
	f.refuseEdits("message to edit not found")
	second := sentID(t, send, "--track", "nightly", "Nightly", "done")
	if second == first {
		t.Fatalf("still message %d after it was deleted", first)
	}
	if !same(f.calls(), "sendMessage", "editMessageText", "sendMessage") {
		t.Fatalf("calls = %v", f.calls())
	}
	rec, err := track.Load(filepath.Join(os.Getenv("XDG_STATE_HOME"), "morse"), "nightly")
	if err != nil || rec.MessageID != second {
		t.Errorf("label points at %+v (%v), want %d", rec, err, second)
	}
}

// A message cannot move between topics, so a new topic means a new message
// there, not an edit of the old one where nobody is looking.
func TestTrackedSendToAnotherTopicStartsANewMessage(t *testing.T) {
	f := newFake(t)
	sentID(t, send, "--track", "nightly", "--topic", "7", "Nightly", "running")
	sentID(t, send, "--track", "nightly", "--topic", "9", "Nightly", "done")
	if !same(f.calls(), "sendMessage", "sendMessage") {
		t.Fatalf("calls = %v", f.calls())
	}
	if p := f.payload(t, 1); p["message_thread_id"] != float64(9) {
		t.Errorf("second send went to topic %v, want 9", p["message_thread_id"])
	}
}

// An explicit id means that message: if it is gone the edit fails, rather than
// a new message appearing that nobody asked for.
func TestEditByIDDoesNotReplaceAGoneMessage(t *testing.T) {
	f := newFake(t)
	f.refuseEdits("message to edit not found")
	err := cmdEdit(context.Background(), noConfig(t), []string{"55", "Nightly", "done"}, &strings.Builder{})
	if err == nil {
		t.Fatal("expected the edit to fail")
	}
	if !same(f.calls(), "editMessageText") {
		t.Errorf("calls = %v, want no send", f.calls())
	}
}

// edit --track takes no --topic and no --silent, so the label's record and
// the nature of an edit decide where a replacement goes and how loudly — and
// the format has to survive the trip.
func TestEditTrackReplacesQuietlyInTheLabelsTopic(t *testing.T) {
	f := newFake(t)
	sentID(t, send, "--track", "nightly", "--topic", "7", "Nightly", "running")
	f.refuseEdits("message to edit not found")
	sentID(t, edit, "--track", "nightly", "--code", "Nightly", "exit 0")

	if !same(f.calls(), "sendMessage", "editMessageText", "sendMessage") {
		t.Fatalf("calls = %v", f.calls())
	}
	p := f.payload(t, 2)
	if p["message_thread_id"] != float64(7) || p["disable_notification"] != true || p["text"] != "<b>Nightly</b>\n<pre>exit 0</pre>" {
		t.Errorf("replacement = %v", p)
	}
}

// A rewrite replaces the markup with the text, so the format has to reach the
// edit, not only the first send.
func TestTrackedRewriteKeepsTheFormat(t *testing.T) {
	f := newFake(t)
	sentID(t, send, "--track", "build", "--code", "Build", "step 1")
	sentID(t, send, "--track", "build", "--code", "Build", "step 2")
	if p := f.payload(t, 1); p["text"] != "<b>Build</b>\n<pre>step 2</pre>" {
		t.Errorf("edit text = %q", p["text"])
	}
}

// An unknown name has to stop the send, not deliver it to General.
func TestSendToAnUnknownTopicNameSendsNothing(t *testing.T) {
	f := newFake(t)
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("telegram:\n  topics:\n    ci: 12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := cmdSend(context.Background(), cfg, []string{"--topic", "backup", "Backup", "done"}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), `no topic named "backup"`) {
		t.Fatalf("err = %v", err)
	}
	if len(f.Requests) != 0 {
		t.Errorf("calls = %v, want none", f.calls())
	}

	// And a known one reaches Telegram as its id.
	if err := cmdSend(context.Background(), cfg, []string{"--topic", "CI", "Build", "red"}, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if p := f.payload(t, 0); p["message_thread_id"] != float64(12) {
		t.Errorf("topic = %v, want 12", p["message_thread_id"])
	}
}

func TestSendToAMissingTopicNamesIt(t *testing.T) {
	newFake(t) // for the credentials and state; the server below replaces it
	srv := testutil.NewFakeAPI(t).
		Handle("POST", "/bottok/sendMessage", 400,
			`{"ok":false,"error_code":400,"description":"Bad Request: message thread not found"}`).
		Start()
	t.Setenv("MORSE_API_URL", srv.URL())
	err := cmdSend(context.Background(), noConfig(t), []string{"--topic", "999", "Backup", "done"}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "has no topic 999") {
		t.Fatalf("err = %v", err)
	}
}

func TestSendFileGoesToTheTopic(t *testing.T) {
	f := newFake(t)
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("412 files\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentID(t, send, "--file", path, "--topic", "7", "Nightly report")
	if !same(f.calls(), "sendDocument") {
		t.Fatalf("calls = %v", f.calls())
	}
	if !strings.Contains(string(f.Requests[0].Body), "name=\"message_thread_id\"\r\n\r\n7\r\n") {
		t.Error("the upload does not carry the topic")
	}
}

func labelRecord(t *testing.T, label string) (*track.Record, error) {
	t.Helper()
	return track.Load(filepath.Join(os.Getenv("XDG_STATE_HOME"), "morse"), label)
}

// "finished" answers "started" by label, without the caller holding the id.
func TestReplyToALabel(t *testing.T) {
	f := newFake(t)
	started := sentID(t, send, "--track", "backup", "Backup", "started")
	sentID(t, send, "--reply-to", "backup", "Backup", "finished")
	reply, _ := f.payload(t, 1)["reply_parameters"].(map[string]any)
	if reply["message_id"] != float64(started) {
		t.Errorf("reply_parameters = %v, want message %d", reply, started)
	}
}

func TestReplyToAnUnknownLabelSendsNothing(t *testing.T) {
	f := newFake(t)
	err := cmdSend(context.Background(), noConfig(t), []string{"--reply-to", "backup", "Backup", "finished"}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "--reply-to") {
		t.Fatalf("err = %v", err)
	}
	if len(f.Requests) != 0 {
		t.Errorf("calls = %v", f.calls())
	}
}

// Pinned when the message is new, not on every rewrite of it.
func TestPinOnceForATrackedStatus(t *testing.T) {
	f := newFake(t)
	sentID(t, send, "--track", "status", "--pin", "Status", "1")
	sentID(t, send, "--track", "status", "--pin", "Status", "2")
	if !same(f.calls(), "sendMessage", "pinChatMessage", "editMessageText") {
		t.Fatalf("calls = %v", f.calls())
	}
	// A replacement is a new message, and pinned again.
	f.refuseEdits("message to edit not found")
	sentID(t, send, "--track", "status", "--pin", "Status", "3")
	if !same(f.calls(), "sendMessage", "pinChatMessage", "editMessageText", "editMessageText", "sendMessage", "pinChatMessage") {
		t.Fatalf("calls = %v", f.calls())
	}
}

// The message went out, so the label still has to point at it — and the error
// has to say that it went out, and which right is missing.
func TestPinRefusedStillRecordsTheMessage(t *testing.T) {
	f := newFake(t)
	f.mu.Lock()
	f.pinRefusal = "not enough rights to manage pinned messages in the chat"
	f.mu.Unlock()
	err := cmdSend(context.Background(), noConfig(t), []string{"--track", "status", "--pin", "Status", "1"}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "delivered, but not pinned") || !strings.Contains(err.Error(), "pin messages") {
		t.Fatalf("err = %v", err)
	}
	if rec, err := labelRecord(t, "status"); err != nil || rec.MessageID != 101 {
		t.Errorf("label = %+v, %v; want message 101", rec, err)
	}
}

func TestPhotoAndFileTogetherAreRefused(t *testing.T) {
	f := newFake(t)
	err := cmdSend(context.Background(), noConfig(t), []string{"--file", "a", "--photo", "b", "T"}, &strings.Builder{})
	if err == nil || len(f.Requests) != 0 {
		t.Fatalf("err = %v, calls = %v", err, f.calls())
	}
}

// An upload cannot be rewritten in place, so a tracked photo is a new message
// each time, and the label follows it.
func TestTrackedPhotoIsANewMessageEachTime(t *testing.T) {
	f := newFake(t)
	path := filepath.Join(t.TempDir(), "graph.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentID(t, send, "--track", "graph", "--photo", path, "Disk")
	second := sentID(t, send, "--track", "graph", "--photo", path, "Disk")
	if !same(f.calls(), "sendPhoto", "sendPhoto") {
		t.Fatalf("calls = %v", f.calls())
	}
	if rec, _ := labelRecord(t, "graph"); rec == nil || rec.MessageID != second {
		t.Errorf("label = %+v, want %d", rec, second)
	}
}

func TestNoPreviewSurvivesATrackedRewrite(t *testing.T) {
	f := newFake(t)
	sentID(t, send, "--track", "ci", "--no-preview", "CI", "https://ci.example/1")
	sentID(t, edit, "--track", "ci", "--no-preview", "CI", "https://ci.example/2")
	for i := range 2 {
		if _, ok := f.payload(t, i)["link_preview_options"]; !ok {
			t.Errorf("request %d (%s) has no link_preview_options", i, f.calls()[i])
		}
	}
}

// Deleting by label removes the message and forgets the label, so the next
// tracked send starts a fresh line instead of failing to edit a gone one.
func TestDeleteByLabelForgetsIt(t *testing.T) {
	f := newFake(t)
	id := sentID(t, send, "--track", "status", "Status", "running")
	if err := cmdDelete(context.Background(), noConfig(t), []string{"--track", "status"}, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if !same(f.calls(), "sendMessage", "deleteMessages") {
		t.Fatalf("calls = %v", f.calls())
	}
	if ids, _ := f.payload(t, 1)["message_ids"].([]any); len(ids) != 1 || ids[0] != float64(id) {
		t.Errorf("deleted %v, want [%d]", ids, id)
	}
	if _, err := labelRecord(t, "status"); err == nil {
		t.Error("the label is still recorded")
	}
}

func TestDeleteByLabelFromAnotherChatDeletesNothing(t *testing.T) {
	f := newFake(t)
	if err := remember("status", 77, 999, 0, "Status", "x"); err != nil {
		t.Fatal(err)
	}
	err := cmdDelete(context.Background(), noConfig(t), []string{"--track", "status"}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "nothing deleted") {
		t.Fatalf("err = %v", err)
	}
	if len(f.Requests) != 0 {
		t.Errorf("calls = %v", f.calls())
	}
	if _, err := labelRecord(t, "status"); err != nil {
		t.Error("the label was forgotten though nothing was deleted")
	}
}

func TestDeleteByIDs(t *testing.T) {
	f := newFake(t)
	for _, bad := range [][]string{{}, {"abc"}, {"0"}, {"5", "--track", "x"}} {
		if err := cmdDelete(context.Background(), noConfig(t), bad, &strings.Builder{}); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if err := cmdDelete(context.Background(), noConfig(t), []string{"5", "6"}, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if ids, _ := f.payload(t, 0)["message_ids"].([]any); len(ids) != 2 {
		t.Errorf("deleted %v, want [5 6]", ids)
	}
}
