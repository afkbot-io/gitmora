package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"time"

	"workgraph.local/platform/internal/collector/gitrepo"
	"workgraph.local/platform/internal/jsonwire"
)

// Reader starts a local metadata API process, or connects to an explicitly selected daemon control
// socket. It cannot start a model turn, approve a tool, resume a thread or read transcript files itself.
type Reader struct {
	Executable string
	Socket     string
}

type protocol struct {
	input io.Writer
	lines *bufio.Scanner
	id    int
	bytes int
}

func (p *protocol) send(value any) error { return json.NewEncoder(p.input).Encode(value) }
func (p *protocol) call(method string, params any, destination any) error {
	p.id++
	if err := p.send(struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{p.id, method, params}); err != nil {
		return ErrUnavailable
	}
	for notifications := 0; notifications < 200; notifications++ {
		if !p.lines.Scan() {
			return ErrUnavailable
		}
		line := p.lines.Bytes()
		p.bytes += len(line)
		if p.bytes > 64*1024*1024 {
			return ErrProtocol
		}
		var response struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if jsonwire.DecodeExternalObject(line, &response) != nil {
			return ErrProtocol
		}
		if response.Method != "" {
			// Never answer a server-initiated request (especially approvals).
			if response.ID != nil {
				return ErrProtocol
			}
			continue
		}
		if response.ID == nil || *response.ID != p.id || len(response.Error) > 0 || len(response.Result) == 0 {
			return ErrProtocol
		}
		if jsonwire.DecodeExternalObject(response.Result, destination) != nil {
			return ErrProtocol
		}
		return nil
	}
	return ErrProtocol
}

// Read acquires a bounded ascending history, fenced by metadata before and after pagination.
// Callers must check repository consent before invoking it.
func (r Reader) Read(parent context.Context, threadID, root string) (thread Thread, entries []Entry, err error) {
	if !filepath.IsAbs(r.Executable) || !filepath.IsAbs(root) || !locator(threadID) ||
		(r.Socket != "" && !filepath.IsAbs(r.Socket)) {
		return thread, nil, ErrProtocol
	}
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return thread, nil, ErrProtocol
	}
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	args := []string{"app-server", "--stdio"}
	if r.Socket != "" {
		args = []string{"app-server", "proxy", "--sock", r.Socket}
	}
	command := exec.CommandContext(ctx, r.Executable, args...)
	command.Dir = physicalRoot
	command.Stderr = io.Discard
	input, err := command.StdinPipe()
	if err != nil {
		return thread, nil, ErrUnavailable
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return thread, nil, ErrUnavailable
	}
	if err = command.Start(); err != nil {
		input.Close()
		output.Close()
		return thread, nil, ErrUnavailable
	}
	defer func() {
		input.Close()
		cancel()
		_ = command.Wait()
		if parent.Err() != nil {
			err = parent.Err()
		} else if ctx.Err() == context.DeadlineExceeded {
			err = context.DeadlineExceeded
		}
	}()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64*1024), 20*1024*1024)
	p := protocol{input: input, lines: scanner}
	var initialized struct {
		UserAgent string `json:"userAgent"`
	}
	if err = p.call("initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "gitmora-capture", "title": "Gitmora", "version": "1"},
		"capabilities": map[string]bool{"experimentalApi": true},
	}, &initialized); err != nil {
		return thread, nil, err
	}
	if initialized.UserAgent == "" || len(initialized.UserAgent) > 1024 {
		return thread, nil, ErrProtocol
	}
	if err = p.send(map[string]string{"method": "initialized"}); err != nil {
		return thread, nil, ErrUnavailable
	}
	readThread := func() (Thread, error) {
		var response struct {
			Thread Thread `json:"thread"`
		}
		e := p.call("thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, &response)
		if e != nil {
			return Thread{}, e
		}
		t := response.Thread
		cwd, e := filepath.EvalSymlinks(t.CWD)
		if e != nil || !filepath.IsAbs(t.CWD) || t.ID != threadID || t.CreatedAt < 1 {
			return Thread{}, ErrProtocol
		}
		repository, e := gitrepo.DiscoverLocal(ctx, cwd)
		if e != nil || repository.Root != physicalRoot {
			return Thread{}, ErrProtocol
		}
		t.CWD = physicalRoot
		return t, nil
	}
	thread, err = readThread()
	if err != nil {
		return thread, nil, err
	}
	cursor := ""
	seen := map[string]bool{}
	for pages := 0; pages < 1000; pages++ {
		params := map[string]any{"threadId": threadID, "limit": 100, "sortDirection": "asc"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var response struct {
			Data []Entry `json:"data"`
			Next *string `json:"nextCursor"`
		}
		if err = p.call("thread/items/list", params, &response); err != nil {
			return thread, nil, err
		}
		if response.Data == nil || len(response.Data) > 100 || len(entries)+len(response.Data) > 10000 {
			return thread, nil, ErrProtocol
		}
		entries = append(entries, response.Data...)
		if response.Next == nil || *response.Next == "" {
			final, e := readThread()
			if e != nil {
				return thread, nil, e
			}
			if final != thread {
				return thread, nil, ErrProtocol
			}
			return thread, entries, nil
		}
		cursor = *response.Next
		if len(cursor) > 4096 || seen[cursor] || len(response.Data) == 0 {
			return thread, nil, ErrProtocol
		}
		seen[cursor] = true
	}
	return thread, nil, ErrProtocol
}
