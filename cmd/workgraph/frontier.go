package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/collector/gitrepo"
	"workgraph.local/platform/internal/collector/sqlite"
	"workgraph.local/platform/internal/jsonwire"
)

type frontierIDs []string

type checkpointBindings []domain.CheckpointBinding

func (b *checkpointBindings) String() string { return "checkpoint positions" }
func (b *checkpointBindings) Set(value string) error {
	parts := strings.Split(value, ":")
	if len(parts) != 2 || !domain.ValidFrontierID(parts[0]) || len(*b) >= domain.MaxPendingFrontiers {
		return domain.ErrInvalid
	}
	end, err := strconv.Atoi(parts[1])
	if err != nil || end < 1 || end > 5000 || strconv.Itoa(end) != parts[1] {
		return domain.ErrInvalid
	}
	*b = append(*b, domain.CheckpointBinding{ID: parts[0], MessageEnd: end})
	return nil
}

func (f *frontierIDs) String() string { return strings.Join(*f, ",") }
func (f *frontierIDs) Set(id string) error {
	if !domain.ValidFrontierID(id) || len(*f) >= domain.MaxPendingFrontiers {
		return domain.ErrInvalid
	}
	*f = append(*f, id)
	return nil
}

func configureFrontier(parent context.Context, args []string, input io.Reader, output io.Writer) error {
	if len(args) == 0 || args[0] == "--help" {
		_, err := fmt.Fprintln(output, "Usage: gitmora frontier begin | end | list | get --id <hash> | apply --id <hash> | resolve --id <hash> --end <full-SHA> --reason <text> | abandon --id <hash> --reason <text>\nbegin/end read {agent,source_session_id,call_id} JSON from stdin. Started tools never infer a missing end. capture --frontier binds; apply resolves only the frozen interval.")
		return err
	}
	command := args[0]
	flags := flag.NewFlagSet("frontier "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var id, end, reason string
	var sync bool
	physical := flags.Bool("physical-worktree", false, "Isolate native-source queries to the physical current worktree")
	switch command {
	case "begin", "end", "list":
	case "get", "apply":
		flags.StringVar(&id, "id", "", "Exact frontier hash")
		if command == "apply" {
			flags.BoolVar(&sync, "sync", false, "Attempt bounded delivery of already eligible context after association")
		}
	case "resolve":
		flags.StringVar(&id, "id", "", "Exact frontier hash")
		flags.StringVar(&end, "end", "", "Explicit full Git SHA")
		flags.StringVar(&reason, "reason", "", "Reason for explicit boundary")
	case "abandon":
		flags.StringVar(&id, "id", "", "Exact frontier hash")
		flags.StringVar(&reason, "reason", "", "Reason for abandoning association")
	default:
		return domain.ErrInvalid
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return domain.ErrInvalid
	}
	if (command == "get" || command == "apply" || command == "resolve" || command == "abandon") && !domain.ValidFrontierID(id) {
		return domain.ErrInvalid
	}
	if command == "resolve" && !capture.ValidCommit(end) {
		return domain.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	discover := gitrepo.Discover
	if *physical {
		discover = gitrepo.DiscoverLocal
	}
	repository, err := discover(ctx, ".")
	if err != nil {
		return err
	}
	if disabled, e := repository.CollectionDisabled(); e != nil {
		return e
	} else if disabled && (command == "begin" || command == "apply") {
		return domain.ErrDisabled
	}
	if _, err := os.Stat(repository.LedgerPath()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.ErrNotInitialized
		}
		return err
	}
	store, err := sqlite.Open(ctx, repository.LedgerPath())
	if err != nil {
		return err
	}
	defer store.Close()
	service := application.NewService(store, nil, time.Now)
	worktree := repository.WorktreeKey()
	var result any
	switch command {
	case "begin", "end":
		raw, err := io.ReadAll(io.LimitReader(input, 8193))
		if err != nil {
			return err
		}
		if len(raw) > 8192 {
			return domain.ErrInvalid
		}
		var request struct {
			Agent   string `json:"agent"`
			Session string `json:"source_session_id"`
			Call    string `json:"call_id"`
		}
		if jsonwire.DecodeObject(raw, &request) != nil {
			return domain.ErrInvalid
		}
		source := domain.NativeSource{Worktree: worktree, Agent: request.Agent, SessionID: request.Session}
		head, err := repository.HeadID(ctx)
		if err != nil {
			return err
		}
		if command == "begin" {
			heads, e := repository.ReferenceHeads(ctx)
			if e != nil {
				return e
			}
			result, err = service.BeginFrontierWithHeads(ctx, source, request.Call, head, heads)
		} else {
			result, err = service.EndFrontier(ctx, source, request.Call, head)
		}
		if err != nil {
			return err
		}
	case "list":
		entries, err := service.PendingFrontiers(ctx, worktree)
		if err != nil {
			return err
		}
		result = struct {
			Frontiers []domain.Frontier `json:"frontiers"`
		}{entries}
	case "get":
		result, err = service.Frontier(ctx, worktree, id)
	case "apply":
		f, err := service.Frontier(ctx, worktree, id)
		if err != nil {
			return err
		}
		if f.After == nil || f.TargetSessionID == "" || f.Abandoned {
			return domain.ErrConflict
		}
		if !f.Applied {
			heads, scoped, err := service.FrontierHeads(ctx, worktree, f.ID)
			if err != nil {
				return err
			}
			var commits []string
			if scoped {
				commits, err = repository.CheckpointWindow(ctx, f.Before, *f.After, heads)
			} else {
				commits, err = repository.HeadWindow(ctx, f.Before, *f.After)
			}
			if err != nil {
				return err
			}
			if err := service.ApplyFrontier(ctx, worktree, f, commits); err != nil {
				return err
			}
		}
		result = map[string]string{"id": id, "status": "applied"}
		if sync {
			delivery, cancelDelivery := context.WithTimeout(ctx, 2*time.Second)
			_, _ = drain(delivery, store) // Association is durable; failed delivery retains its outbox.
			cancelDelivery()
		}
	case "resolve":
		if err := repository.VerifyCommit(ctx, end); err != nil {
			return err
		}
		result, err = service.ResolveFrontier(ctx, worktree, id, end, reason)
	case "abandon":
		err = service.AbandonFrontier(ctx, worktree, id, reason)
		result = map[string]string{"id": id, "status": "abandoned"}
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
