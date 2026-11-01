package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/collector/gitrepo"
	"workgraph.local/platform/internal/collector/sqlite"
)

func recoverRanges(parent context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("recover-ranges", flag.ContinueOnError)
	flags.SetOutput(out)
	id := flags.String("session", "", "Last local recording UUID in the conversation")
	native := flags.String("codex-thread", "", "Recorded native Codex thread ID")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if flags.NArg() != 0 || (*id == "") == (*native == "") || (*id != "" && !capture.ValidID(*id)) {
		return domain.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	repo, err := gitrepo.Discover(ctx, ".")
	if err != nil {
		return err
	}
	disabled, err := repo.CollectionDisabled()
	if err != nil {
		return err
	}
	if disabled {
		return domain.ErrDisabled
	}
	if _, err := os.Stat(repo.LedgerPath()); err != nil {
		return domain.ErrNotInitialized
	}
	ledger, err := sqlite.Open(ctx, repo.LedgerPath())
	if err != nil {
		return err
	}
	defer ledger.Close()
	config, err := ledger.Config(ctx)
	if err != nil {
		return err
	}
	if config.Disabled {
		return domain.ErrDisabled
	}
	if *native != "" {
		source := domain.NativeSource{Worktree: repo.WorktreeKey(), Agent: "codex", SessionID: "codex:" + *native}
		if source.Validate() != nil || !config.AllowsAgent(source.Agent) {
			return domain.ErrInvalid
		}
		if err := ledger.Within(ctx, func(tx application.Transaction) error {
			stream, found, err := tx.Stream(source.Key(config.RepositoryID))
			if err != nil {
				return err
			}
			if !found {
				return domain.ErrNotInitialized
			}
			entry, found, err := tx.Find(stream.ActiveKey)
			if err != nil {
				return err
			}
			if !found {
				return domain.ErrConflict
			}
			*id = entry.State().Snapshot.SessionID
			return nil
		}); err != nil {
			return err
		}
	}
	head, err := repo.HeadID(ctx)
	if err != nil {
		return err
	}
	commits, err := repo.HeadWindow(ctx, "", head)
	if err != nil {
		return err
	}
	count, err := application.NewService(ledger, nil, time.Now).RecoverCommitRanges(ctx, repo.WorktreeKey(), *id, commits)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"ranges": count, "basis": "recovered_git_output", "status": "queued"})
}
