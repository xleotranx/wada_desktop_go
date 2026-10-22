package opencode

import (
	"cmp"
	"context"
	"errors"
	"net/url"
	"strings"
)

// provider is one of the server's integrations: a model provider and the
// ways to connect it.
type provider struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Methods     []method     `json:"methods"`
	Connections []connection `json:"connections"`
}

// method is one way to connect a provider: "key", "oauth" and "external"
// take a form; "env" reads environment variables the server already has;
// "command" runs a program and isn't offered.
type method struct {
	Type  string   `json:"type"`
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Form  []field  `json:"form"`
	Names []string `json:"names"` // env
}

// field is one question of a method's form. Only string fields are asked;
// a form with any other kind is not offered.
type field struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Placeholder string   `json:"placeholder"`
	Default     string   `json:"default"`
	Required    bool     `json:"required"`
	Hidden      bool     `json:"hidden"`
	Custom      bool     `json:"custom"` // free text is allowed besides Options
	Options     []option `json:"options"`
	When        []when   `json:"when"`
}

type option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// when shows a field only while another field's answer is (op "eq") or
// isn't ("neq") a value.
type when struct {
	Key   string `json:"key"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

// connection is how a provider is connected now: a stored credential
// (Type "credential", removable by ID) or an environment variable ("env").
type connection struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Label  string `json:"label"`
	Method string `json:"method"`
	Name   string `json:"name"`
	Status *struct {
		Message string `json:"message"`
	} `json:"status"`
}

// attempt is a browser sign-in under way. Mode "auto" finishes by itself;
// "code" waits for the code the provider shows.
type attempt struct {
	ID           string `json:"attemptID"`
	URL          string `json:"url"`
	Instructions string `json:"instructions"`
	Mode         string `json:"mode"`
}

// offered reports whether the app can ask m's form.
func (m method) offered() bool {
	switch m.Type {
	case "key", "oauth", "external":
	default:
		return false
	}
	for _, f := range m.Form {
		if f.Type != "string" {
			return false
		}
	}
	return true
}

// title names m for a choice between methods.
func (m method) title() string {
	if m.Label != "" {
		return m.Label
	}
	switch m.Type {
	case "key":
		return "API key"
	case "oauth":
		return "Sign in"
	}
	return m.Type
}

// visible reports whether f is asked, given the answers so far.
func (f field) visible(answer map[string]string) bool {
	if f.Hidden {
		return false
	}
	for _, w := range f.When {
		v, _ := w.Value.(string)
		if (answer[w.Key] == v) != (w.Op == "eq") {
			return false
		}
	}
	return true
}

// formAnswer is what the server receives for m's form: the visible fields'
// answers and hidden fields' defaults. It reports the first required field
// left empty.
func formAnswer(m method, answer map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range m.Form {
		v := strings.TrimSpace(answer[f.Key])
		switch {
		case f.Hidden:
			v = f.Default
		case !f.visible(answer):
			continue
		case v == "":
			v = f.Default
		}
		if v == "" {
			if f.Required {
				return nil, errors.New(cmp.Or(f.Title, f.Key) + " is required")
			}
			continue
		}
		out[f.Key] = v
	}
	return out, nil
}

func integrationPath(id string, rest ...string) string {
	p := "/integration/" + url.PathEscape(id)
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}

func (c *client) providers(ctx context.Context) ([]provider, error) {
	var out struct {
		Data []provider `json:"data"`
	}
	err := c.decode(ctx, "GET", "/integration", nil, &out)
	return out.Data, err
}

func (c *client) connectKey(ctx context.Context, id, key string, answer map[string]string) error {
	return c.decode(ctx, "POST", integrationPath(id, "connect", "key"),
		map[string]any{"key": key, "answer": answer}, nil)
}

func (c *client) connectExternal(ctx context.Context, id, methodID string, answer map[string]string) error {
	return c.decode(ctx, "POST", integrationPath(id, "connect", "external"),
		map[string]any{"methodID": methodID, "answer": answer}, nil)
}

func (c *client) beginSignIn(ctx context.Context, id, methodID string, answer map[string]string) (attempt, error) {
	var out struct {
		Data attempt `json:"data"`
	}
	err := c.decode(ctx, "POST", integrationPath(id, "connect", "oauth"),
		map[string]any{"methodID": methodID, "answer": answer}, &out)
	return out.Data, err
}

// signInStatus is "pending", "complete" or "failed", with the server's
// message for a failure.
func (c *client) signInStatus(ctx context.Context, id, attemptID string) (string, string, error) {
	var out struct {
		Data struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"data"`
	}
	err := c.decode(ctx, "GET", integrationPath(id, "connect", "oauth", attemptID), nil, &out)
	return out.Data.Status, out.Data.Message, err
}

func (c *client) finishSignIn(ctx context.Context, id, attemptID, code string) error {
	return c.decode(ctx, "POST", integrationPath(id, "connect", "oauth", attemptID, "complete"),
		map[string]any{"code": code}, nil)
}

func (c *client) cancelSignIn(ctx context.Context, id, attemptID string) error {
	return c.decode(ctx, "DELETE", integrationPath(id, "connect", "oauth", attemptID), nil, nil)
}

func (c *client) removeCredential(ctx context.Context, credentialID string) error {
	return c.decode(ctx, "DELETE", "/credential/"+url.PathEscape(credentialID), nil, nil)
}
