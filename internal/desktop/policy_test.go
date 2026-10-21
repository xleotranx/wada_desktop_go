package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSetShellGatePreservesRulePrecedence(t *testing.T) {
	askRule := map[string]any{"action": "shell", "resource": "*", "effect": "ask"}
	restrictions := []any{
		map[string]any{"action": "shell", "resource": "rm *", "effect": "deny"},
		map[string]any{"action": "shell", "resource": "git status", "effect": "allow"},
		map[string]any{"action": "read", "resource": "*", "effect": "deny"},
	}
	for _, name := range []string{"new gate", "existing trailing gate", "duplicate gates"} {
		t.Run(name, func(t *testing.T) {
			rules := append([]any(nil), restrictions...)
			if name != "new gate" {
				rules = append(rules, askRule)
			}
			if name == "duplicate gates" {
				rules = append([]any{askRule}, rules...)
			}
			doc := map[string]any{"permissions": rules}
			if !setShellGate(doc, true) {
				t.Fatal("the general ask rule was not put before the existing rules")
			}
			want := append([]any{askRule}, restrictions...)
			if got := permissionRules(doc); !reflect.DeepEqual(got, want) {
				t.Fatalf("rules = %+v, want %+v", got, want)
			}
			if setShellGate(doc, true) {
				t.Fatal("enabling the normalized gate again rewrote it")
			}
			if !setShellGate(doc, false) || !reflect.DeepEqual(permissionRules(doc), restrictions) {
				t.Fatalf("clearing ask changed existing rules: %+v", permissionRules(doc))
			}
		})
	}
}

func TestSetShellGatePreservesExplicitWildcardAllow(t *testing.T) {
	doc := map[string]any{"permissions": []any{
		map[string]any{"action": "shell", "resource": "*", "effect": "allow"},
	}}
	setShellGate(doc, true)
	if got := shellGate(doc); got != "full" {
		t.Fatalf("explicit allow was overridden: %q", got)
	}
	if setShellGate(doc, true) {
		t.Fatal("the normalized gate rewrote an explicit allow")
	}
}

func TestSetPermissionAskPreservesUnreadableConfig(t *testing.T) {
	for _, ask := range []bool{false, true} {
		dir := t.TempDir()
		pth := filepath.Join(dir, "opencode.json")
		original := []byte(`{"model":"keep-me"}`)
		if err := os.WriteFile(pth, original, 0200); err != nil {
			t.Fatal(err)
		}
		if _, err := os.ReadFile(pth); err == nil {
			t.Skip("process can read files without read permission")
		} else if !os.IsPermission(err) {
			t.Fatal("test requires a file that cannot be read")
		}
		err := setPermissionAsk(dir, ask)
		if chmodErr := os.Chmod(pth, 0600); chmodErr != nil {
			t.Fatal(chmodErr)
		}
		if !os.IsPermission(err) {
			t.Errorf("ask %t: got %v, want the read permission error", ask, err)
		}
		got, readErr := os.ReadFile(pth)
		if readErr != nil || string(got) != string(original) {
			t.Errorf("ask %t: config changed to %q (%v)", ask, got, readErr)
		}
	}
}

func TestSetPermissionAskRejectsNull(t *testing.T) {
	for _, ask := range []bool{false, true} {
		dir := t.TempDir()
		pth := filepath.Join(dir, "opencode.json")
		if err := os.WriteFile(pth, []byte("null\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := permissionLevel(dir); err == nil {
			t.Error("null document was displayed as a valid shell policy")
		}
		if err := setPermissionAsk(dir, ask); err == nil {
			t.Errorf("ask %t: null document was accepted", ask)
		}
		if got, err := os.ReadFile(pth); err != nil || string(got) != "null\n" {
			t.Fatalf("config changed to %q (%v)", got, err)
		}
	}
}

func TestRemoteShellGateReturnsEffectiveLevel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"type":"document","path":"/srv/app/opencode.json","info":{"permissions":[{"action":"shell","resource":"*","effect":"allow"}]}}]`))
	})
	written := make(chan struct{}, 1)
	mux.HandleFunc("POST /api/experimental/fs/write", func(w http.ResponseWriter, r *http.Request) {
		written <- struct{}{}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	level, err := remoteSetPermissionAsk(context.Background(), serverConn{url: srv.URL}, "/srv/app", true)
	if err != nil || level != "full" {
		t.Fatalf("effective policy = %q (%v), want preserved explicit allow", level, err)
	}
	select {
	case <-written:
	default:
		t.Fatal("the missing ask rule was not saved")
	}
}
