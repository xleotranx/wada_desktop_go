package opencode

import (
	"fmt"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"

	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

// TestMain lets the test binary stand in for `opencode serve`.
func TestMain(m *testing.M) {
	if os.Getenv("WADA_FAKE_OPENCODE") == "1" {
		fakeServe()
		return
	}
	os.Exit(m.Run())
}

func fakeServe() {
	i := slices.Index(os.Args, "--port")
	if len(os.Args) < 2 || os.Args[1] != "serve" || i < 0 || i+1 >= len(os.Args) {
		os.Exit(2)
	}
	password := os.Getenv("OPENCODE_SERVER_PASSWORD")
	http.ListenAndServe("127.0.0.1:"+os.Args[i+1], http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pw, _ := r.BasicAuth(); pw != password || password == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/info":
			fmt.Fprint(w, `{"version":"managed"}`)
		case "/api/event":
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
}

func TestManagedServerRunsAndStopsWithThePlugin(t *testing.T) {
	t.Setenv("WADA_FAKE_OPENCODE", "1")
	var p *Plugin
	h := kerneltest.New(t, (&kittest.Host{}).New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{exe: os.Args[0], dir: t.TempDir()} }))
	waitFor(t, h, "ready", func() bool { return p.Status().State == agent.Ready })
	if p.Status().Version != "managed" {
		t.Fatalf("status %+v", p.Status())
	}
	c, err := p.current()
	if err != nil || c.cmd == nil || c.cmd.Dir != p.dir {
		t.Fatalf("managed client %+v, %v", c, err)
	}
	if err := h.Disable("opencode"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for c.cmd.ProcessState == nil {
		if time.Now().After(deadline) {
			t.Fatal("the managed server outlived the plugin")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
