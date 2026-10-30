package mentions

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugintest"

	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

// png is the signature of a PNG file, which is all the type check reads.
var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func start(t *testing.T, extra ...kernel.Factory) (*kerneltest.Harness, *Plugin, *plugintest.Files, *plugintest.Chat, *kittest.Host) {
	t.Helper()
	files := plugintest.NewFiles(map[string]string{
		"src/main.go": "package main\n",
		"src/mail.go": "package main // mail\n",
		"README.md":   "# readme\n",
		"logo.png":    string(png),
		"tool.bin":    "\x00\x01\x02binary",
	})
	ctl, host := &plugintest.Chat{}, &kittest.Host{}
	var p *Plugin
	h := kerneltest.New(t, append([]kernel.Factory{files.New, ctl.New, host.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} })}, extra...)...)
	h.Flush()
	return h, p, files, ctl, host
}

// completion is what the composer sees of the plugin.
func completion(h *kerneltest.Harness) chat.Completion {
	return kernel.Items(h, chat.Completions)[0].Value
}

func labels(cs []chat.Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Detail+"|"+c.Label)
	}
	return out
}

// send runs the outgoing pipeline as the chat does, committing what it accepts.
func send(h *kerneltest.Harness, text string) (chat.Draft, error) {
	out, commit, err := kernel.Run(h, chat.Outgoing, chat.Draft{Text: text})
	if err == nil {
		commit()
	}
	return out, err
}

func fileNames(d chat.Draft) []string {
	var out []string
	for _, f := range d.Files {
		out = append(out, f.Name)
	}
	return out
}

func TestTypingListsMatchingFilesWithTheirFolders(t *testing.T) {
	h, p, files, ctl, _ := start(t)
	ctl.Conv.Directory = "/w/app"
	c := completion(h)
	c.Query("ma", true)
	if len(c.Candidates()) != 0 {
		t.Fatal("candidates before the lookup answered")
	}
	h.Flush()
	if got := labels(c.Candidates()); !slices.Equal(got, []string{"src|mail.go", "src|main.go"}) {
		t.Fatalf("candidates %q", got)
	}
	if got := files.Calls(); !slices.Equal(got, []string{"find /w/app|ma"}) {
		t.Fatalf("calls %q", got)
	}
	if first := c.Candidates()[0]; first.ID != "src/mail.go" || first.Icon == nil {
		t.Fatalf("candidate %+v", first)
	}
	c.Query("", false)
	if len(c.Candidates()) != 0 || p.lookup.Running() {
		t.Fatal("the list outlived the word")
	}
}

func TestTypingListsMatchingFoldersToo(t *testing.T) {
	h, _, _, ctl, _ := start(t)
	ctl.Conv.Directory = "/w/app"
	c := completion(h)
	c.Query("s", true)
	h.Flush()
	if got := labels(c.Candidates()); !slices.Equal(got, []string{"|src/", "src|mail.go", "src|main.go"}) {
		t.Fatalf("candidates %q", got)
	}
	if first := c.Candidates()[0]; first.ID != "src/" || first.Icon == nil {
		t.Fatalf("candidate %+v", first)
	}
}

func TestOnlyTheLatestLookupLands(t *testing.T) {
	h, _, _, _, _ := start(t)
	c := completion(h)
	c.Query("ma", true)
	c.Query("main", true)
	h.Flush()
	if got := labels(c.Candidates()); !slices.Equal(got, []string{"src|main.go"}) {
		t.Fatalf("candidates %q", got)
	}
}

func TestAChosenFileGoesWithTheMessageAsItsPath(t *testing.T) {
	h, p, files, ctl, _ := start(t)
	ctl.Conv.Directory = "/w/app"
	c := completion(h)
	text := c.Accept(chat.Candidate{ID: "src/main.go"})
	if text != "@src/main.go " {
		t.Fatalf("text %q", text)
	}
	h.Flush()
	d, err := send(h, "look at "+text+"please")
	if err != nil || !slices.Equal(fileNames(d), []string{"src/main.go"}) {
		t.Fatalf("files %q, %v", fileNames(d), err)
	}
	if f := d.Files[0]; f.MIME != "text/plain" || string(f.Data) != "package main\n" {
		t.Fatalf("file %+v", f)
	}
	if got := files.Calls(); !slices.Equal(got, []string{"read /w/app|src/main.go"}) {
		t.Fatalf("calls %q", got)
	}
	if d.Text != "look at @src/main.go please" {
		t.Fatalf("the words were rewritten: %q", d.Text)
	}
	if len(p.mentions) != 0 {
		t.Fatal("the mention outlived the message")
	}
	if d, _ = send(h, "again @src/main.go"); len(d.Files) != 0 {
		t.Fatalf("a sent mention attached again: %q", fileNames(d))
	}
}

func TestAMentionWaitsWhenALaterTransformRejectsTheMessage(t *testing.T) {
	h, p, _, ctl, _ := start(t)
	ctl.Conv.Directory = "/w/app"
	text := completion(h).Accept(chat.Candidate{ID: "src/main.go"})
	h.Flush()
	kernel.Transform(h, chat.Outgoing, 2000, func(d chat.Draft) (chat.Draft, func(), error) {
		return d, nil, errors.New("not yet")
	})
	if _, err := send(h, "look at "+text); err == nil || len(p.mentions) != 1 {
		t.Fatalf("rejected send: %v, mentions %d", err, len(p.mentions))
	}
}

func TestAChosenFolderIsOnlyNamedInTheMessage(t *testing.T) {
	h, p, files, ctl, _ := start(t)
	ctl.Conv.Directory = "/w/app"
	text := completion(h).Accept(chat.Candidate{ID: "src/"})
	if text != "@src/ " {
		t.Fatalf("text %q", text)
	}
	h.Flush()
	if len(p.mentions) != 0 {
		t.Fatal("a folder was read as a file")
	}
	if got := files.Calls(); len(got) != 0 {
		t.Fatalf("a folder was read: %q", got)
	}
	d, err := send(h, "look in "+text+"please")
	if err != nil || len(d.Files) != 0 || d.Text != "look in @src/ please" {
		t.Fatalf("draft %+v, %v", d, err)
	}
}

func TestOnlyFilesTheTextStillNamesGoOutInTheOrderNamed(t *testing.T) {
	h, _, _, _, _ := start(t)
	c := completion(h)
	for _, id := range []string{"src/main.go", "README.md", "src/mail.go"} {
		c.Accept(chat.Candidate{ID: id})
	}
	h.Flush()
	d, err := send(h, "compare @src/mail.go with @README.md, ignoring the rest")
	if err != nil || !slices.Equal(fileNames(d), []string{"src/mail.go", "README.md"}) {
		t.Fatalf("files %q, %v", fileNames(d), err)
	}
}

func TestAMessageWaitsForFilesStillBeingRead(t *testing.T) {
	h, p, _, _, _ := start(t)
	c := completion(h)
	c.Accept(chat.Candidate{ID: "src/main.go"})
	c.Accept(chat.Candidate{ID: "README.md"})
	if _, err := send(h, "@src/main.go"); err == nil || !strings.Contains(err.Error(), "main.go is still being read") {
		t.Fatalf("a message went out with its file unread: %v", err)
	}
	if len(p.mentions) != 2 {
		t.Fatal("a refused draft lost its mentions")
	}
	if _, err := send(h, "@README.md"); err == nil {
		t.Fatal("the second file was not waited for")
	}
	h.Flush()
	if d, err := send(h, "@src/main.go"); err != nil || len(d.Files) != 1 {
		t.Fatalf("after reading: %q, %v", fileNames(d), err)
	}
}

func TestAFileNobodyNamedDoesNotHoldTheMessageBack(t *testing.T) {
	h, p, _, _, _ := start(t)
	completion(h).Accept(chat.Candidate{ID: "src/main.go"})
	if d, err := send(h, "no mention here"); err != nil || len(d.Files) != 0 {
		t.Fatalf("an unnamed file held the message back: %v", err)
	}
	h.Flush() // the read lands for a message that has gone: nothing is kept
	if len(p.mentions) != 0 {
		t.Fatal("a read landed after its message went out")
	}
}

func TestFilesThatCannotGoAreReportedAndKeepTheirText(t *testing.T) {
	h, p, files, _, host := start(t)
	files.Tree["big.txt"] = bytes.Repeat([]byte("x"), maxMentionBytes+1)
	c := completion(h)
	for _, id := range []string{"big.txt", "tool.bin", "missing.go"} {
		c.Accept(chat.Candidate{ID: id})
	}
	h.Flush()
	if len(host.Toasts) != 3 || len(p.mentions) != 0 {
		t.Fatalf("toasts %q, mentions %d", host.Toasts, len(p.mentions))
	}
	if d, err := send(h, "@big.txt @tool.bin @missing.go"); err != nil || len(d.Files) != 0 || d.Text != "@big.txt @tool.bin @missing.go" {
		t.Fatalf("draft %+v, %v", d, err)
	}
}

func TestImagesGoWithTheirType(t *testing.T) {
	h, _, _, _, _ := start(t)
	completion(h).Accept(chat.Candidate{ID: "logo.png"})
	h.Flush()
	d, _ := send(h, "see @logo.png")
	if len(d.Files) != 1 || d.Files[0].MIME != "image/png" || d.Files[0].Name != "logo.png" {
		t.Fatalf("files %+v", d.Files)
	}
}

func TestTheAgentsFileTypesLimitWhatGoes(t *testing.T) {
	h, _, _, _, host := start(t, (&textOnly{}).New)
	completion(h).Accept(chat.Candidate{ID: "logo.png"})
	h.Flush()
	if d, _ := send(h, "@logo.png"); len(d.Files) != 1 {
		t.Fatal("an image was refused before the agent said what it takes")
	}
	if err := h.Enable("text-only"); err != nil {
		t.Fatal(err)
	}
	completion(h).Accept(chat.Candidate{ID: "src/main.go"})
	completion(h).Accept(chat.Candidate{ID: "logo.png"})
	h.Flush()
	d, _ := send(h, "@src/main.go and @logo.png")
	if !slices.Equal(fileNames(d), []string{"src/main.go"}) || len(host.Toasts) != 1 {
		t.Fatalf("files %q toasts %q", fileNames(d), host.Toasts)
	}
}

// textOnly is a backend that takes only text files.
type textOnly struct{}

func (f *textOnly) New() kernel.Plugin { return f }
func (*textOnly) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "text-only", Provides: []kernel.Named{agent.FileTypesKey}, DefaultOff: true}
}
func (t *textOnly) Setup(c *kernel.Context) error {
	return kernel.Provide[agent.FileTypes](c, agent.FileTypesKey, t)
}
func (*textOnly) Accepts(mime string) bool { return strings.HasPrefix(mime, "text/") }
func (*textOnly) Summary() string          { return "text files" }

func TestAnotherConversationStartsWithNoMentions(t *testing.T) {
	h, p, _, ctl, _ := start(t)
	completion(h).Accept(chat.Candidate{ID: "README.md"})
	h.Flush()
	ctl.Open(agent.Session{ID: "other"})
	if len(p.mentions) != 0 {
		t.Fatal("mentions followed the user to another conversation")
	}
	if d, _ := send(h, "@README.md"); len(d.Files) != 0 {
		t.Fatalf("files %q", fileNames(d))
	}
	completion(h).Accept(chat.Candidate{ID: "README.md"})
	ctl.Open(agent.Session{ID: "other", Title: "renamed"}) // the same conversation, changed
	h.Flush()
	if len(p.mentions) != 1 {
		t.Fatal("a rename dropped the mentions")
	}
}

func TestAMessageTakesAtMostTenMentions(t *testing.T) {
	h, _, files, _, host := start(t)
	c := completion(h)
	for i := range maxMentions + 1 {
		id := string(rune('a'+i)) + ".txt"
		files.Tree[id] = []byte(id)
		c.Accept(chat.Candidate{ID: id})
	}
	h.Flush()
	if len(host.Toasts) != 1 {
		t.Fatalf("toasts %q", host.Toasts)
	}
	var all []string
	for i := range maxMentions + 1 {
		all = append(all, "@"+string(rune('a'+i))+".txt")
	}
	if d, _ := send(h, strings.Join(all, " ")); len(d.Files) != maxMentions {
		t.Fatalf("%d files went out", len(d.Files))
	}
}

func TestAFailingLookupIsReportedOncePerWord(t *testing.T) {
	h, _, files, _, host := start(t)
	files.Fail["Find"] = errors.New("backend down")
	c := completion(h)
	c.Query("a", true)
	h.Flush()
	c.Query("ab", true)
	h.Flush()
	if len(host.Toasts) != 1 || len(c.Candidates()) != 0 {
		t.Fatalf("toasts %q", host.Toasts)
	}
	c.Query("", false)
	c.Query("c", true)
	h.Flush()
	if len(host.Toasts) != 2 {
		t.Fatalf("a new word did not report: %q", host.Toasts)
	}
}

func TestMentionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		text string
		at   int
	}{
		{"@src/main.go", 0},
		{"see @src/main.go", 4},
		{"see @src/main.go.", 4},
		{"see @src/main.go, then", 4},
		{"see (@src/main.go)", -1}, // a mention starts a word
		{"see @src/main.gopher", -1},
		{"see @src/main.go.bak", -1},
		{"x@src/main.go", -1},
		{"@src/main.gox @src/main.go", 14},
		{"see @src/main.go\nnext", 4},
		{"see @src/ma", -1},
		{"", -1},
	} {
		if got := mentionedAt(tc.text, "src/main.go"); got != tc.at {
			t.Errorf("%q: %d, want %d", tc.text, got, tc.at)
		}
	}
}

func TestIsBlockedWhereTheBackendCannotReadFiles(t *testing.T) {
	ctl, host := &plugintest.Chat{}, &kittest.Host{}
	var p *Plugin
	h := kerneltest.New(t, ctl.New, host.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	for _, st := range h.Plugins() {
		if st.Manifest.ID == "mentions" && (st.State != kernel.Blocked || !slices.Equal(st.Missing, []string{"agent.files"})) {
			t.Fatalf("status %+v", st)
		}
	}
	if len(kernel.Items(h, chat.Completions)) != 0 {
		t.Fatal("a blocked plugin offers completions")
	}
}
