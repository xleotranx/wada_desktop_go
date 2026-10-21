package pi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestMain lets the test binary stand in for `pi`.
func TestMain(m *testing.M) {
	if os.Getenv("WADA_FAKE_PI") == "1" {
		fakePi()
		return
	}
	os.Exit(m.Run())
}

// fakePi speaks enough of Pi's RPC mode for the plugin's tests. A new session
// is written to the session folder with its first prompt.
func fakePi() {
	args := os.Args[1:]
	if slices.Contains(args, "--version") {
		fmt.Println("9.9.9")
		return
	}
	arg := func(name string) string {
		if i := slices.Index(args, name); i >= 0 && i+1 < len(args) {
			return args[i+1]
		}
		return ""
	}
	dir, file := arg("--session-dir"), arg("--session")
	id := "new-1"
	if file != "" {
		raw, _ := os.ReadFile(file)
		var h header
		json.Unmarshal([]byte(strings.SplitN(string(raw), "\n", 2)[0]), &h)
		id = h.ID
	} else if !slices.Contains(args, "--no-session") {
		file = filepath.Join(dir, "--new--", "2026_new-1.jsonl")
	}
	out := json.NewEncoder(os.Stdout)
	var mu sync.Mutex
	write := func(v any) { mu.Lock(); out.Encode(v); mu.Unlock() }
	appendEntry := func(lines ...string) {
		if file == "" {
			return
		}
		os.MkdirAll(filepath.Dir(file), 0o700)
		if _, err := os.Stat(file); err != nil {
			wd, _ := os.Getwd()
			os.WriteFile(file, []byte(`{"type":"session","version":3,"id":"`+id+`","cwd":"`+wd+`"}`+"\n"), 0o600)
		}
		f, _ := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
		for _, l := range lines {
			f.WriteString(l + "\n")
		}
		f.Close()
	}
	write(map[string]any{"type": "extension_ui_request", "id": "n1", "method": "notify", "message": "loaded"})
	in := bufio.NewScanner(os.Stdin)
	answers := map[string]chan map[string]any{}
	for in.Scan() {
		var cmd map[string]any
		if json.Unmarshal(in.Bytes(), &cmd) != nil {
			continue
		}
		kind, _ := cmd["type"].(string)
		reply := func(data any) {
			write(map[string]any{"type": "response", "id": cmd["id"], "command": kind, "success": true, "data": data})
		}
		switch kind {
		case "extension_ui_response":
			if ch := answers[cmd["id"].(string)]; ch != nil {
				ch <- cmd
			}
		case "get_state":
			reply(map[string]any{"sessionId": id, "sessionFile": file, "thinkingLevel": "low",
				"model": map[string]any{"id": "m1", "provider": "p", "name": "Model One"}})
		case "get_available_models":
			reply(map[string]any{"models": []any{
				map[string]any{"id": "m1", "name": "Model One", "provider": "p", "reasoning": true,
					"thinkingLevelMap": map[string]any{"off": nil, "max": "max"}, "cost": map[string]any{"input": 0, "output": 0}},
				map[string]any{"id": "m2", "name": "Model Two", "provider": "p", "reasoning": false,
					"cost": map[string]any{"input": 1, "output": 2}},
			}})
		case "set_session_name":
			reply(nil)
			name, _ := cmd["name"].(string)
			appendEntry(`{"type":"session_info","id":"info1","parentId":null,"name":"` + name + `"}`)
			write(map[string]any{"type": "session_info_changed", "name": name})
		case "prompt":
			text, _ := cmd["message"].(string)
			if text == "reject-contract-send" {
				f, _ := os.OpenFile(os.Getenv("WADA_FAKE_PI_ATTEMPTS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if f != nil {
					f.WriteString("attempt\n")
					f.Close()
				}
				write(map[string]any{"type": "response", "id": cmd["id"], "command": kind, "success": false, "error": "rejected"})
				continue
			}
			if text == "/handled" || text == "/queued" {
				reply(map[string]any{"disposition": strings.TrimPrefix(text, "/")})
				continue
			}
			reply(map[string]any{"disposition": "started"})
			answer := "Hello"
			write(map[string]any{"type": "agent_start"})
			if text == "fail please" {
				write(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant"}})
				write(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "error", "errorMessage": "401: Invalid credential"}})
				write(map[string]any{"type": "auto_retry_end", "success": false, "finalError": "401: Invalid credential (after 3 tries)"})
				write(map[string]any{"type": "agent_settled", "aborted": false})
				continue
			}
			if slices.Contains([]string{"confirm please", "select please", "input please", "editor please"}, text) {
				method := strings.TrimSuffix(text, " please")
				ch := make(chan map[string]any, 1)
				answers["dlg1"] = ch
				write(map[string]any{"type": "extension_ui_request", "id": "dlg1", "method": method, "title": "Run it?", "message": "rm -rf build", "options": []string{"first", "second"}, "prefill": "initial"})
				go func() {
					r := <-ch
					result := fmt.Sprintf("confirmed=%v", r["confirmed"])
					if method != "confirm" {
						result = fmt.Sprintf("value=%v cancelled=%v", r["value"], r["cancelled"])
					}
					finish(write, result, appendEntry, text)
				}()
				continue
			}
			finish(write, answer, appendEntry, text)
		default:
			reply(nil)
		}
	}
}

// finish streams one answer with a tool call and settles the run.
func finish(write func(any), answer string, appendEntry func(...string), prompt string) {
	write(map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}})
	write(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_start", "contentIndex": 0}})
	for _, part := range []string{answer[:len(answer)/2], answer[len(answer)/2:]} {
		write(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": part}})
	}
	write(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_end", "contentIndex": 0, "content": answer}})
	write(map[string]any{"type": "tool_execution_start", "toolCallId": "t1", "toolName": "bash", "args": map[string]any{"command": "ls"}})
	write(map[string]any{"type": "tool_execution_end", "toolCallId": "t1", "toolName": "bash",
		"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "a\nb"}}}, "isError": true})
	write(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "stopReason": "stop"}})
	write(map[string]any{"type": "agent_end"})
	write(map[string]any{"type": "agent_settled", "aborted": false})
	appendEntry(
		`{"type":"message","id":"u1","parentId":null,"message":{"role":"user","content":"`+prompt+`"}}`,
		`{"type":"message","id":"a1","parentId":"u1","message":{"role":"assistant","content":[{"type":"text","text":"`+answer+`"}],"provider":"p","model":"m1","usage":{"output":3,"cost":{"total":0.5}}}}`,
	)
}
