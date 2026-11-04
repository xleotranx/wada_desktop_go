package cowork

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

func readURL(ctx context.Context, req request) (string, error) {
	transport := publicTransport()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if (r.URL.Scheme != "http" && r.URL.Scheme != "https") || r.URL.User != nil {
			return errors.New("unsupported redirect")
		}
		return nil
	}}
	return readURLWithClient(ctx, req, client)
}

func readURLWithClient(ctx context.Context, req request, client *http.Client) (string, error) {
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", errors.New("provide an HTTP(S) URL without embedded credentials")
	}

	call, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	call.Header.Set("User-Agent", "WadaCowork/1.0")
	response, err := client.Do(call)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("source returned HTTP %d", response.StatusCode)
	}
	mime := strings.ToLower(response.Header.Get("Content-Type"))
	if !strings.HasPrefix(mime, "text/") && !strings.HasPrefix(mime, "application/json") && !strings.HasPrefix(mime, "application/xml") {
		return "", errors.New("URL must return a text page; download binary documents through a supported source")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil {
		return "", err
	}
	if len(data) > 2*1024*1024 {
		return "", errors.New("source exceeds the 2 MiB page limit")
	}
	text := string(data)
	if strings.Contains(mime, "html") {
		text = webText(text)
	}
	req.URL = response.Request.URL.String()
	return pageText(req, text)
}

func webText(source string) string {
	tokens := html.NewTokenizer(strings.NewReader(source))
	var out strings.Builder
	skip := 0
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return strings.TrimSpace(out.String())
		case html.StartTagToken:
			name, _ := tokens.TagName()
			tag := string(name)
			if tag == "script" || tag == "style" || tag == "noscript" {
				skip++
			}
			if skip == 0 && (tag == "p" || tag == "div" || tag == "li" || tag == "br" || tag == "h1" || tag == "h2" || tag == "tr") {
				out.WriteByte('\n')
			}
		case html.EndTagToken:
			name, _ := tokens.TagName()
			tag := string(name)
			if (tag == "script" || tag == "style" || tag == "noscript") && skip > 0 {
				skip--
			}
			if skip == 0 {
				out.WriteByte(' ')
			}
		case html.TextToken:
			if skip == 0 {
				text := strings.Join(strings.Fields(string(tokens.Text())), " ")
				if text != "" {
					out.WriteString(text)
					out.WriteByte(' ')
				}
			}
		case html.SelfClosingTagToken:
		case html.DoctypeToken:
		case html.CommentToken:
		}
	}
}
