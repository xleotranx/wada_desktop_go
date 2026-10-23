package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"wada-desktop-go/internal/contract/agent"
)

// The OpenAPI document the pinned OpenCode release serves at /openapi.json,
// kept beside the tests so a change of version needs a new copy (see
// scripts/pin-opencode-spec.sh). Event payloads are opaque strings in it, so
// TestDecodeEveryKnownEvent covers those instead.
func specFile() string { return "testdata/openapi-" + version + ".json" }

type apiSpec struct {
	Paths      map[string]map[string]*specOp `json:"paths"`
	Components struct {
		Schemas map[string]*specSchema `json:"schemas"`
	} `json:"components"`
}

type specOp struct {
	ID         string `json:"operationId"`
	Parameters []struct {
		Name string `json:"name"`
		In   string `json:"in"`
	} `json:"parameters"`
	RequestBody *struct {
		Content map[string]struct {
			Schema *specSchema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema *specSchema `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

type specSchema struct {
	Ref        string                 `json:"$ref"`
	Properties map[string]*specSchema `json:"properties"`
	Items      *specSchema            `json:"items"`
	AnyOf      []*specSchema          `json:"anyOf"`
	OneOf      []*specSchema          `json:"oneOf"`
	AllOf      []*specSchema          `json:"allOf"`
}

func loadSpec(t *testing.T) *apiSpec {
	t.Helper()
	raw, err := os.ReadFile(specFile())
	if err != nil {
		t.Fatalf("no pinned spec for OpenCode %s: %v", version, err)
	}
	var s apiSpec
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

// variants is every concrete schema s stands for: its references followed
// and its unions opened.
func (s *apiSpec) variants(sc *specSchema) []*specSchema {
	var out []*specSchema
	var walk func(sc *specSchema, depth int)
	walk = func(sc *specSchema, depth int) {
		if sc == nil || depth > 16 {
			return
		}
		if sc.Ref != "" {
			walk(s.Components.Schemas[strings.TrimPrefix(sc.Ref, "#/components/schemas/")], depth+1)
			return
		}
		out = append(out, sc)
		for _, u := range slices.Concat(sc.AnyOf, sc.OneOf, sc.AllOf) {
			walk(u, depth+1)
		}
	}
	walk(sc, 0)
	return out
}

// property is the schema of name in any variant of sc. open reports that sc
// names no properties at all, such as a free-form object, so any name goes.
func (s *apiSpec) property(sc *specSchema, name string) (prop *specSchema, open bool) {
	open = true
	for _, v := range s.variants(sc) {
		if v.Properties != nil {
			open = false
			if p := v.Properties[name]; p != nil {
				return p, false
			}
		}
	}
	return nil, open
}

func (s *apiSpec) items(sc *specSchema) *specSchema {
	for _, v := range s.variants(sc) {
		if v.Items != nil {
			return v.Items
		}
	}
	return nil
}

// match finds the operation a request reaches: path parameters take one
// segment, a trailing * the rest, and the most literal path wins.
func (s *apiSpec) match(method, path string) (string, *specOp) {
	got := strings.Split(strings.Trim(path, "/"), "/")
	best, bestScore := "", -1
	for p, ops := range s.Paths {
		if ops[strings.ToLower(method)] == nil {
			continue
		}
		want := strings.Split(strings.Trim(strings.TrimPrefix(p, "/api"), "/"), "/")
		score, ok := 0, true
		for i, w := range want {
			if w == "*" { // the rest of the path, at least one segment
				ok = i < len(got)
				break
			}
			if i >= len(got) || (!strings.HasPrefix(w, "{") && w != got[i]) {
				ok = false
				break
			}
			if !strings.HasPrefix(w, "{") {
				score++
			}
			if i == len(want)-1 && len(got) != len(want) {
				ok = false
			}
		}
		if ok && score > bestScore {
			best, bestScore = p, score
		}
	}
	if best == "" {
		return "", nil
	}
	return best, s.Paths[best][strings.ToLower(method)]
}

// checkValue reports the keys of a JSON value the schema does not name.
func (s *apiSpec) checkValue(at string, v any, sc *specSchema, errs *[]string) {
	switch v := v.(type) {
	case map[string]any:
		for k, sub := range v {
			prop, open := s.property(sc, k)
			if open {
				continue
			}
			if prop == nil {
				*errs = append(*errs, at+"."+k)
				continue
			}
			s.checkValue(at+"."+k, sub, prop, errs)
		}
	case []any:
		if items := s.items(sc); items != nil {
			for _, sub := range v {
				s.checkValue(at+"[]", sub, items, errs)
			}
		}
	}
}

var rawMessage = reflect.TypeFor[json.RawMessage]()

// checkType reports the JSON fields of t the schema does not name.
func (s *apiSpec) checkType(at string, t reflect.Type, sc *specSchema, errs *[]string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == rawMessage:
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		if items := s.items(sc); items != nil {
			s.checkType(at+"[]", t.Elem(), items, errs)
		}
	case t.Kind() == reflect.Struct:
		for f := range t.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if !f.IsExported() || name == "-" || name == "" {
				continue
			}
			prop, open := s.property(sc, name)
			if open {
				continue
			}
			if prop == nil {
				*errs = append(*errs, at+"."+name)
				continue
			}
			s.checkType(at+"."+name, f.Type, prop, errs)
		}
	}
}

func jsonSchema(op *specOp) *specSchema {
	for _, code := range []string{"200", "201"} {
		if c, ok := op.Responses[code].Content["application/json"]; ok {
			return c.Schema
		}
	}
	return nil
}

// replies are the shapes the client decodes, by operation.
var replies = map[string]reflect.Type{
	"session.list":              reflect.TypeFor[sessionListReply](),
	"session.create":            reflect.TypeFor[sessionCreateReply](),
	"fs.find":                   reflect.TypeFor[fileFindReply](),
	"session.diff":              reflect.TypeFor[sessionDiffReply](),
	"session.message.list":      reflect.TypeFor[messageListReply](),
	"model.list":                reflect.TypeFor[modelListReply](),
	"model.default":             reflect.TypeFor[defaultModelReply](),
	"agent.list":                reflect.TypeFor[agentListReply](),
	"integration.list":          reflect.TypeFor[providerListReply](),
	"integration.oauth.connect": reflect.TypeFor[signInReply](),
	"integration.oauth.status":  reflect.TypeFor[signInStatusReply](),
}

type recorded struct{ method, uri, body string }

// TestClientSpeaksThePinnedSpec makes every call the client has against a
// server that records them, and checks each against the pinned OpenAPI
// document: the route, the query parameters, the request body's fields and
// the fields the client reads from the answer.
func TestClientSpeaksThePinnedSpec(t *testing.T) {
	spec := loadSpec(t)
	var mu sync.Mutex
	var seen []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, recorded{r.Method, r.URL.RequestURI(), string(body)})
		mu.Unlock()
		if r.URL.Path == "/api/info" {
			fmt.Fprint(w, `{"data":{"version":"`+version+`"}}`)
			return
		}
		fmt.Fprint(w, `{"data":{}}`)
	}))
	defer srv.Close()
	c := &client{base: srv.URL + "/api", password: "pw", http: srv.Client()}
	ctx := context.Background()

	id := agent.SessionID("ses_1")
	_ = c.waitReady(ctx, time.Second)
	_, _ = c.sessions(ctx, agent.SessionQuery{Cursor: "c", Search: "s", Directory: "/w"})
	_, _ = c.create(ctx, agent.CreateSession{Directory: "/w", Mode: "build"})
	_ = c.send(ctx, agent.Send{Session: id, Mode: "plan", Text: "hi", Files: []agent.File{{Name: "a.png", MIME: "image/png", Data: []byte{1}}}})
	_, _ = c.find(ctx, "/w", "main", 5)
	_, _ = c.read(ctx, "/w", "docs/a.md")
	_ = c.abort(ctx, id)
	_ = c.reply(ctx, agent.Reply{Session: id, Approval: "per_1", Decision: agent.Once})
	_ = c.rename(ctx, id, "New")
	_ = c.delete(ctx, id)
	_ = c.rewind(ctx, id, "msg_1")
	_, _ = c.changes(ctx, id)
	_, _ = c.messages(ctx, id)
	_, _ = c.models(ctx)
	_, _ = c.defaultModel(ctx)
	_ = c.setModel(ctx, id, agent.ModelRef{Provider: "p", ID: "m", Variant: "high"})
	_, _ = c.modes(ctx)
	_, _ = c.providers(ctx)
	_ = c.connectKey(ctx, "anthropic", "k", map[string]string{"region": "us"})
	_ = c.connectExternal(ctx, "anthropic", "env", map[string]string{"region": "us"})
	_, _ = c.beginSignIn(ctx, "openai", "browser", map[string]string{"region": "us"})
	_, _, _ = c.signInStatus(ctx, "openai", "att_1")
	_ = c.finishSignIn(ctx, "openai", "att_1", "code")
	_ = c.cancelSignIn(ctx, "openai", "att_1")
	_ = c.removeCredential(ctx, "cred_1")
	_ = c.events(ctx, func() {}, func(wireEvent) {})

	reached := map[string]bool{}
	for _, r := range seen {
		u, err := url.Parse(r.uri)
		if err != nil {
			t.Fatal(err)
		}
		route := r.method + " " + strings.TrimPrefix(u.Path, "/api")
		path, op := spec.match(r.method, strings.TrimPrefix(u.EscapedPath(), "/api"))
		if op == nil {
			t.Errorf("%s: no such route in the spec", route)
			continue
		}
		reached[op.ID] = true
		for key := range u.Query() {
			name, _, _ := strings.Cut(key, "[") // deepObject: location[directory]
			if !slices.ContainsFunc(op.Parameters, func(p struct {
				Name string `json:"name"`
				In   string `json:"in"`
			}) bool {
				return p.In == "query" && p.Name == name
			}) {
				t.Errorf("%s: query parameter %q is not in %s", route, key, path)
			}
		}
		if strings.TrimSpace(r.body) != "" {
			var body any
			if err := json.Unmarshal([]byte(r.body), &body); err != nil {
				t.Fatalf("%s: body is not JSON: %v", route, err)
			}
			if op.RequestBody == nil {
				if m, ok := body.(map[string]any); !ok || len(m) > 0 {
					t.Errorf("%s: sends a body the spec does not take", route)
				}
				continue
			}
			var errs []string
			spec.checkValue("body", body, op.RequestBody.Content["application/json"].Schema, &errs)
			for _, e := range errs {
				t.Errorf("%s: sends %s, which the spec does not name", route, e)
			}
		}
	}
	for opID, typ := range replies {
		if !reached[opID] {
			t.Errorf("%s: the client did not call it", opID)
			continue
		}
		var op *specOp
		for _, ops := range spec.Paths {
			for _, o := range ops {
				if o.ID == opID {
					op = o
				}
			}
		}
		sc := jsonSchema(op)
		if sc == nil {
			t.Errorf("%s: the spec gives no JSON answer", opID)
			continue
		}
		var errs []string
		spec.checkType("answer", typ, sc, &errs)
		for _, e := range errs {
			t.Errorf("%s: reads %s, which the spec does not name", opID, e)
		}
	}
}
