package cowork

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/documents"
)

func operation(t *testing.T, dir, action, input string) string {
	t.Helper()
	out, err := workspaceOperation(t.Context(), dir, "", nil, action, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestDailyDocumentWorkCreatesEditsReadsAndFindsEvidence(t *testing.T) {
	dir := t.TempDir()
	operation(t, dir, "write", `{"path":"reports/weekly.md","content":"# Weekly report\nBudget: 1200\nNext: review"}`)
	operation(t, dir, "update", `{"path":"reports/weekly.md","old_text":"Budget: 1200","new_text":"Budget: 1350"}`)
	got := operation(t, dir, "read", `{"path":"reports/weekly.md"}`)
	if !strings.Contains(got, "Budget: 1350") || strings.Contains(got, "Budget: 1200") {
		t.Fatal(got)
	}
	found := operation(t, dir, "search", `{"query":"budget"}`)
	if !strings.Contains(found, `"path":"reports/weekly.md"`) || !strings.Contains(found, `"line":2`) {
		t.Fatal(found)
	}
	list := operation(t, dir, "list", `{"path":"reports"}`)
	if !strings.Contains(list, "weekly.md") {
		t.Fatal(list)
	}
	if _, err := workspaceOperation(t.Context(), dir, "", nil, "update", json.RawMessage(`{"path":"reports/weekly.md","old_text":"missing","new_text":"bad"}`)); err == nil {
		t.Fatal("ambiguous/stale edit accepted")
	}
}
func TestFolderBoundariesExcludeAppDataDependenciesAndSymlinkEscapes(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	app := filepath.Join(dir, "app-data")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "sessions.json"), []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../private.md", "/etc/passwd", ".git/config", ".wada/chats.json", "node_modules/package.json", "escape/new/file.md", "app-data/sessions.json"} {
		if _, err := workPath(dir, app, path); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	out, err := workspaceOperation(t.Context(), dir, app, nil, "list", json.RawMessage(`{}`))
	if err != nil || strings.Contains(out, "app-data") || strings.Contains(out, "escape") {
		t.Fatalf("protected files listed: %s/%v", out, err)
	}
	inbox := filepath.Join(app, "workspaces", "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := workPath(inbox, app, "note.md"); err != nil {
		t.Fatal("private Inbox cannot create deliverables:", err)
	}
}
func TestWritingDoesNotMislabelNativeOfficeFormats(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"report.docx", "slides.pptx", "budget.xlsx", "report.pdf", "run.sh"} {
		raw, _ := json.Marshal(map[string]any{"path": name, "content": "plain text"})
		if _, err := workspaceOperation(t.Context(), dir, "", nil, "write", raw); err == nil {
			t.Fatalf("fake native/executable output accepted: %s", name)
		}
	}
}

type fakeReader struct{ path string }

func (r *fakeReader) Read(_ context.Context, path string) (documents.Source, error) {
	r.path = path
	return documents.Source{Path: path, Markdown: "# Source\nTotal: 250"}, nil
}
func (*fakeReader) Ingest(context.Context, string) (documents.Result, error) {
	return documents.Result{}, nil
}
func TestOfficeDocumentsUseTheExistingReader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "budget.xlsx")
	if err := os.WriteFile(path, []byte("native document bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &fakeReader{}
	reader := func(context.Context) (documents.Reader, error) { return r, nil }
	out, err := workspaceOperation(t.Context(), dir, "", reader, "read", json.RawMessage(`{"path":"budget.xlsx"}`))
	if err != nil || !strings.Contains(out, "Total: 250") || r.path == "" {
		t.Fatalf("reader was not used: %s/%v", out, err)
	}
}
func TestPaginationCountsUnicodeCharactersAndCancellationPreventsWrites(t *testing.T) {
	dir := t.TempDir()
	operation(t, dir, "write", `{"path":"note.txt","content":"Một hai ba"}`)
	out := operation(t, dir, "read", `{"path":"note.txt","offset":4,"limit":3}`)
	if !strings.Contains(out, `"text":"hai"`) || !strings.Contains(out, `"more":true`) {
		t.Fatal(out)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := workspaceOperation(ctx, dir, "", nil, "write", json.RawMessage(`{"path":"never.md","content":"no"}`)); err == nil {
		t.Fatal("cancelled mutation executed")
	}
	if _, err := os.Stat(filepath.Join(dir, "never.md")); !os.IsNotExist(err) {
		t.Fatal("cancelled write created a file")
	}
}
func TestArithmeticIsDeterministicAndCannotExecuteCode(t *testing.T) {
	for expression, value := range map[string]string{"(1200 + 350) * 1.1": "1705", "-10 + 25/5": "-5", "15 % 4": "3"} {
		got, err := calculate(expression)
		if err != nil || !strings.Contains(got, `"value":`+value) {
			t.Fatalf("%s: %s/%v", expression, got, err)
		}
	}
	for _, expression := range []string{"1/0", "open(\"secret\")", "os.Exit(0)", "1 << 4", "true", "1e999"} {
		if _, err := calculate(expression); err == nil {
			t.Fatalf("invalid calculation accepted: %s", expression)
		}
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestReadingAURLReturnsSourceTextWithoutRunningPageScripts(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
			t.Fatal("unexpected authenticated or mutating web request")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(`<html><style>secretCSS</style><script>runSomething()</script><h1>Quarterly update</h1><p>Revenue &amp; costs</p></html>`)), Request: r}, nil
	})}
	out, err := readURLWithClient(t.Context(), request{URL: "https://example.com/report"}, client)
	if err != nil || !strings.Contains(out, "Quarterly update") || !strings.Contains(out, "example.com/report") || strings.Contains(out, "runSomething") || strings.Contains(out, "secretCSS") {
		t.Fatalf("source extraction: %s/%v", out, err)
	}
	for _, url := range []string{"file:///etc/passwd", "javascript:run()", "https://user:password@example.com"} {
		if _, err := readURLWithClient(t.Context(), request{URL: url}, client); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
}

func TestPublicPageReadsCannotReachAppServersOrMetadata(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "100.64.0.1"} {
		if publicAddress(netip.MustParseAddr(ip)) {
			t.Fatalf("private destination accepted: %s", ip)
		}
	}
	if !publicAddress(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
}
