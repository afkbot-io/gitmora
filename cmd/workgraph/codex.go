package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"workgraph.local/platform/internal/agent/codex"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/collector/gitrepo"
	"workgraph.local/platform/internal/collector/sqlite"
	"workgraph.local/platform/internal/jsonwire"
)

type codexRequest struct {
	Event   string `json:"hook_event_name"`
	Session string `json:"session_id"`
	CWD     string `json:"cwd"`
	Model   string `json:"model"`
	Call    string `json:"tool_use_id"`
}

// The hook is observational. Every failure leaves a neutral Codex control response and a fixed diagnostic.
func codexHook(parent context.Context, args []string, input io.Reader, output, diagnostic io.Writer) error {
	flags := flag.NewFlagSet("internal-codex-hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	executable := flags.String("codex", "", "Absolute Codex executable")
	socket := flags.String("socket", "", "Existing local app-server socket")
	var err error
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		err = domain.ErrInvalid
	} else {
		var raw []byte
		raw, err = io.ReadAll(io.LimitReader(input, 8*1024*1024+1))
		if err == nil {
			var request codexRequest
			if len(raw) > 8*1024*1024 || jsonwire.DecodeExternalObject(raw, &request) != nil {
				err = domain.ErrInvalid
			} else {
				ctx, cancel := context.WithTimeout(parent, 18*time.Second)
				_, err = collectCodex(ctx, request, codex.Reader{Executable: *executable, Socket: *socket})
				cancel()
			}
		}
	}
	if err != nil && !errors.Is(err, domain.ErrNotInitialized) && !errors.Is(err, domain.ErrDisabled) {
		_, _ = fmt.Fprintln(diagnostic, "Gitmora: local Codex context capture deferred; inspect status and retry agent capture codex")
	}
	_, writeErr := fmt.Fprintln(output, "{}")
	return writeErr
}

type codexResult struct {
	Status       string   `json:"status"`
	Windows      int      `json:"windows"`
	SkippedTypes []string `json:"skipped_types"`
}

func codexCapture(parent context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("agent capture codex", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	executable := flags.String("codex", "", "Codex executable (default: PATH)")
	socket := flags.String("socket", "", "Existing local app-server socket")
	session := flags.String("session", "", "Exact native Codex thread ID")
	model := flags.String("model", "", "Observed model slug, otherwise unknown")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(output, "Usage: gitmora agent capture codex --session <thread-ID> [--codex /absolute/path] [--socket /daemon/control/socket]\nReads through the local app-server metadata API. No model is started.")
	}
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil || flags.NArg() != 0 || *session == "" {
		return domain.ErrInvalid
	}
	selected, err := codexExecutable(*executable)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	result, err := collectCodex(ctx, codexRequest{Event: "Stop", Session: *session, Model: *model, CWD: cwd}, codex.Reader{Executable: selected, Socket: *socket})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func codexExecutable(selected string) (string, error) {
	if selected == "" {
		value, err := exec.LookPath("codex")
		if err != nil {
			return "", codex.ErrUnavailable
		}
		selected = value
	}
	return filepath.Abs(selected)
}

func collectCodex(ctx context.Context, request codexRequest, reader codex.Reader) (codexResult, error) {
	result := codexResult{Status: "recorded", SkippedTypes: []string{}}
	switch request.Event {
	case "SessionStart", "Stop", "PreToolUse", "PostToolUse":
	default:
		return result, domain.ErrInvalid
	}
	if request.Session == "" || !filepath.IsAbs(request.CWD) {
		return result, domain.ErrInvalid
	}
	repository, err := gitrepo.DiscoverLocal(ctx, request.CWD)
	if err != nil {
		return result, domain.ErrNotInitialized
	}
	if disabled, e := repository.CollectionDisabled(); e != nil {
		return result, e
	} else if disabled {
		return result, domain.ErrDisabled
	}
	if _, err = os.Stat(repository.LedgerPath()); errors.Is(err, os.ErrNotExist) {
		return result, domain.ErrNotInitialized
	} else if err != nil {
		return result, err
	}
	store, err := sqlite.Open(ctx, repository.LedgerPath())
	if err != nil {
		return result, err
	}
	defer store.Close()
	config, err := store.Config(ctx)
	if err != nil {
		return result, err
	}
	if !config.AllowsAgent("codex") {
		return result, domain.ErrDisabled
	}
	service := application.NewService(store, nil, time.Now)
	worktree := repository.WorktreeKey()
	source := domain.NativeSource{Worktree: worktree, Agent: "codex", SessionID: "codex:" + request.Session}
	if source.Validate() != nil {
		return result, domain.ErrInvalid
	}
	if request.Event == "PreToolUse" || request.Event == "PostToolUse" {
		head, e := repository.HeadID(ctx)
		if e != nil {
			return result, e
		}
		if request.Event == "PreToolUse" {
			_, err = service.BeginFrontier(ctx, source, request.Call, head)
		} else {
			var frontier domain.Frontier
			frontier, err = service.EndFrontier(ctx, source, request.Call, head)
			if err == nil && frontier.After != nil && frontier.Before == *frontier.After && !frontier.Applied && !frontier.Abandoned {
				err = service.AbandonFrontier(ctx, worktree, frontier.ID, "observed_no_head_change")
			}
		}
		return result, err
	}
	pending, err := service.PendingFrontiers(ctx, worktree)
	if err != nil {
		return result, err
	}
	apply := func(frontier domain.Frontier) error {
		if frontier.TargetSessionID == "" || frontier.After == nil {
			return nil
		}
		commits, e := repository.HeadWindow(ctx, frontier.Before, *frontier.After)
		if e != nil {
			return e
		}
		return service.ApplyFrontier(ctx, worktree, frontier, commits)
	}
	// Recover already bound observations even while the native app-server is unavailable.
	for _, frontier := range pending {
		if frontier.Source.Agent == source.Agent && frontier.Source.SessionID == source.SessionID {
			if err = apply(frontier); err != nil {
				return result, err
			}
		}
	}
	thread, entries, err := reader.Read(ctx, request.Session, repository.Root)
	if err != nil {
		return result, err
	}
	history, err := codex.Normalize(thread, entries, request.Model, "unknown", repository.Root)
	if err != nil {
		return result, err
	}
	result.SkippedTypes = history.SkippedTypes
	lastWindow := map[string]int{}
	for index, window := range history.Windows {
		for _, item := range window.ItemIDs {
			frontier, e := domain.BeginFrontier(config.RepositoryID, source, item, "")
			if e != nil {
				return result, e
			}
			lastWindow[frontier.ID] = index
		}
	}
	for index, window := range history.Windows {
		bindings := []string{}
		for _, frontier := range pending {
			if position, found := lastWindow[frontier.ID]; found && position == index && frontier.After != nil && frontier.TargetSessionID == "" {
				bindings = append(bindings, frontier.ID)
			}
		}
		if _, err = service.RecordNativeFrontiers(ctx, worktree, window.Observation, bindings); err != nil {
			return result, err
		}
		result.Windows++
		for _, id := range bindings {
			frontier, e := service.Frontier(ctx, worktree, id)
			if e != nil {
				return result, e
			}
			if err = apply(frontier); err != nil {
				return result, err
			}
		}
	}
	if result.Windows == 0 {
		result.Status = "no_visible_items"
	}
	delivery, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, _ = drain(delivery, store) // Only previously authorized commit publication; failures remain queued.
	cancel()
	return result, nil
}
