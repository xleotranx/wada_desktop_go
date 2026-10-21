package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// proc is one `pi --mode rpc` child: JSONL commands on stdin, responses and
// events on stdout. It is safe for concurrent use.
type proc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{} // closed when stdout ends
	err   error         // why it ended; read after done

	mu      sync.Mutex
	nextID  int
	pending map[string]chan response
	stderr  tail
}

type response struct {
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

// record is one stdout line, decoded to what routing needs.
type record struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	response
}

// start runs pi in dir with args after "--mode rpc". Every record that is not
// a command response goes to events, on the reader goroutine, in order; events
// receives the process so it can answer before start has returned.
func start(ctx context.Context, exe, dir string, args []string, events func(p *proc, kind string, raw []byte)) (*proc, error) {
	path, err := exec.LookPath(exe)
	if err != nil {
		return nil, fmt.Errorf("pi was not found: %w", err)
	}
	cmd := exec.CommandContext(ctx, path, append([]string{"--mode", "rpc"}, args...)...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &proc{cmd: cmd, stdin: stdin, done: make(chan struct{}), pending: map[string]chan response{}}
	cmd.Stderr = &p.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go p.read(stdout, events)
	return p, nil
}

func (p *proc) read(stdout io.Reader, events func(*proc, string, []byte)) {
	r := bufio.NewReaderSize(stdout, 64*1024)
	var err error
	for {
		var line []byte
		// Split on LF only: U+2028 and U+2029 are valid inside JSON strings.
		line, err = r.ReadBytes('\n')
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > 0 {
			var rec record
			if json.Unmarshal(line, &rec) == nil {
				if rec.Type == "response" && rec.ID != "" {
					p.deliver(rec.ID, rec.response)
				} else if rec.Type != "response" {
					events(p, rec.Type, line)
				}
			}
		}
		if err != nil {
			break
		}
	}
	waitErr := p.cmd.Wait()
	p.mu.Lock()
	if waitErr != nil {
		err = waitErr
	}
	if msg := p.stderr.String(); msg != "" {
		err = fmt.Errorf("%w: %s", err, msg)
	}
	p.err = err
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
	p.mu.Unlock()
	close(p.done)
}

func (p *proc) deliver(id string, r response) {
	p.mu.Lock()
	ch := p.pending[id]
	delete(p.pending, id)
	p.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

// call sends one command and waits for its response.
func (p *proc) call(ctx context.Context, kind string, fields map[string]any, out any) error {
	p.mu.Lock()
	p.nextID++
	id := "w" + strconv.Itoa(p.nextID)
	ch := make(chan response, 1)
	p.pending[id] = ch
	p.mu.Unlock()
	cmd := map[string]any{"id": id, "type": kind}
	for k, v := range fields {
		cmd[k] = v
	}
	if err := p.send(cmd); err != nil {
		p.forget(id)
		return err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return p.exitErr()
		}
		if !r.Success {
			return fmt.Errorf("pi %s: %s", kind, cmpOr(r.Error, "failed"))
		}
		if out != nil && len(r.Data) > 0 && string(r.Data) != "null" {
			if err := json.Unmarshal(r.Data, out); err != nil {
				return fmt.Errorf("pi %s: the answer did not decode", kind)
			}
		}
		return nil
	case <-ctx.Done():
		p.forget(id)
		return ctx.Err()
	}
}

func (p *proc) forget(id string) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

// send writes one record without waiting for an answer.
func (p *proc) send(v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return p.errLocked()
	default:
	}
	_, err = p.stdin.Write(append(line, '\n'))
	return err
}

func (p *proc) exitErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.errLocked()
}

func (p *proc) errLocked() error {
	if p.err != nil && !errors.Is(p.err, io.EOF) {
		return fmt.Errorf("pi exited: %w", p.err)
	}
	return errors.New("pi exited")
}

// close asks pi to shut down by closing stdin, then kills it if it lingers.
func (p *proc) close() {
	p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		p.cmd.Process.Kill()
		<-p.done
	}
}

// tail keeps the last few KiB of stderr for error messages.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
