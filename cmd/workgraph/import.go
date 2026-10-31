package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/collector/gitrepo"
	"workgraph.local/platform/internal/collector/sqlite"
	compat "workgraph.local/platform/internal/compatibility/application"
	compatdomain "workgraph.local/platform/internal/compatibility/domain"
	"workgraph.local/platform/internal/compatibility/gitsource"
)

func configureImport(parent context.Context, args []string, output, diagnostic io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		_, err := fmt.Fprintln(output, "Usage: gitmora import git-ai --commit <full-SHA> [--sync]\n       gitmora import entire --checkpoint <ID> --commit <full-SHA> [--storage refs|branch] [--session <source-session-ID>] [--sync]\nReads local immutable Git objects. Imports remain private until existing publication controls permit delivery.")
		return err
	}
	request := gitsource.Request{System: args[0]}
	if request.System != "git-ai" && request.System != "entire" {
		return compatdomain.ErrInvalid
	}
	flags := flag.NewFlagSet("import "+request.System, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&request.Commit, "commit", "", "Existing full target commit SHA")
	flags.StringVar(&request.SourceRef, "source-ref", "", "Replay a previously selected source ref; requires source-object")
	flags.StringVar(&request.SourceObject, "source-object", "", "Read the previous immutable ref commit even after its ref moves; requires source-ref")
	if request.System == "entire" {
		flags.StringVar(&request.Checkpoint, "checkpoint", "", "Exact canonical checkpoint ID")
		flags.StringVar(&request.Storage, "storage", "refs", "Checkpoint storage: refs or branch")
		flags.StringVar(&request.Session, "session", "", "Select one original source session; default imports every indexed session")
	}
	sync := flags.Bool("sync", false, "Attempt bounded delivery of already eligible context after durable import")
	flags.Usage = func() {
		_, _ = fmt.Fprintf(output, "Usage: gitmora import %s --commit <full-SHA> [flags]\n", request.System)
		flags.SetOutput(output)
		flags.PrintDefaults()
		flags.SetOutput(io.Discard)
	}
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil || flags.NArg() != 0 {
		return compatdomain.ErrInvalid
	}
	if !capture.ValidCommit(request.Commit) || (request.System == "entire" && (!compatdomain.ValidEntireID(request.Checkpoint) || (request.Storage != "refs" && request.Storage != "branch"))) {
		return compatdomain.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	repository, err := gitrepo.DiscoverLocal(ctx, ".")
	if err != nil {
		return err
	}
	if disabled, e := repository.CollectionDisabled(); e != nil {
		return e
	} else if disabled {
		return domain.ErrDisabled
	}
	if _, err := os.Stat(repository.LedgerPath()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.ErrNotInitialized
		}
		return err
	}
	projections, err := gitsource.Acquire(ctx, repository.Root, request)
	if err != nil {
		return err
	}
	plan := []domain.Observation{}
	for _, p := range projections {
		observations, err := compat.Prepare(p)
		if err != nil {
			return err
		}
		plan = append(plan, observations...)
	}
	store, err := sqlite.Open(ctx, repository.LedgerPath())
	if err != nil {
		return err
	}
	defer store.Close()
	service := application.NewService(store, nil, time.Now)
	reply := struct {
		Status   string                  `json:"status"`
		Sessions []string                `json:"session_ids"`
		Windows  int                     `json:"windows"`
		Delivery *application.SyncResult `json:"delivery,omitempty"`
		Sources  []importSource          `json:"sources"`
	}{Status: "recorded", Sessions: []string{}, Windows: len(plan)}
	seenSources := map[string]bool{}
	for _, o := range plan {
		key := o.Origin.Ref + ":" + o.Origin.RefObject
		if !seenSources[key] {
			reply.Sources = append(reply.Sources, importSource{Ref: o.Origin.Ref, Object: o.Origin.RefObject})
			seenSources[key] = true
		}
	}
	for _, observation := range plan {
		id, err := service.RecordNative(ctx, repository.WorktreeKey(), observation)
		if err != nil {
			return err
		}
		reply.Sessions = append(reply.Sessions, id)
	}
	if *sync {
		delivery, cancelDelivery := context.WithTimeout(ctx, 2*time.Second)
		result, err := drain(delivery, store)
		cancelDelivery()
		reply.Delivery = &result
		if err != nil || result.Failed > 0 {
			_, _ = fmt.Fprintln(diagnostic, "Import is recorded locally; eligible delivery remains queued")
		}
	}
	return json.NewEncoder(output).Encode(reply)
}

type importSource struct {
	Ref    string `json:"ref"`
	Object string `json:"object"`
}
