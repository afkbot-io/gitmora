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

	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/retrieval/domain"
	"workgraph.local/platform/internal/transport/nativeapi"
)

// searchCommand uses the same permission-aware endpoint as web and MCP. JSON
// preserves evidence identities and opaque continuation fields for agent callers.
func searchCommand(parent context.Context, args []string, out io.Writer) error {
	const usage = "Usage: gitmora search <query> --server <URL> --project <UUID> [--repository <UUID>] [--mode auto|lexical|hybrid|semantic] [--agent <name>] [--model <id>] [--role <role>] [--commit <SHA>] [--path <path>] [--limit 20] [--after <cursor.json>]\nReturns JSON evidence and next; use next unchanged with --after to continue. Uses the saved account."
	if len(args) == 0 || args[0] == "--help" {
		_, err := fmt.Fprintln(out, usage)
		return err
	}
	q := domain.Query{Text: args[0]}
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", "", "Gitmora server")
	after := flags.String("after", "", "Search cursor JSON file")
	flags.StringVar(&q.ProjectID, "project", "", "Project UUID")
	flags.StringVar(&q.RepositoryID, "repository", "", "Repository UUID")
	flags.StringVar(&q.Mode, "mode", "", "Search mode")
	flags.StringVar(&q.Agent, "agent", "", "Agent integration")
	flags.StringVar(&q.Model, "model", "", "Reported model")
	flags.StringVar(&q.Role, "role", "", "Message role")
	flags.StringVar(&q.CommitSHA, "commit", "", "Exact commit SHA")
	flags.StringVar(&q.FilePath, "path", "", "Exact repository-relative path")
	flags.IntVar(&q.Limit, "limit", 20, "Result page size")
	flags.BoolVar(&q.History, "history", false, "Include older revisions")
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		_, e := fmt.Fprintln(out, usage)
		return e
	} else if err != nil || flags.NArg() != 0 {
		return domain.ErrInvalid
	}
	if *after != "" {
		f, err := os.Open(*after)
		if err != nil {
			return domain.ErrInvalid
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return domain.ErrInvalid
		}
		raw, err := io.ReadAll(io.LimitReader(f, 8193))
		if err != nil || len(raw) > 8192 {
			return domain.ErrInvalid
		}
		q.After = &domain.SearchCursor{}
		if jsonwire.DecodeObject(raw, q.After) != nil {
			return domain.ErrInvalid
		}
	}
	normalized, err := domain.NormalizeQuery(q)
	if err != nil {
		return err
	}
	// Keep the original text so the API applies the same SHA/UUID auto-detection.
	normalized.Text = q.Text
	normalized.CommitPrefix = q.CommitPrefix
	normalized.SessionID = q.SessionID
	normalized.CommitSHA = q.CommitSHA
	raw, _ := json.Marshal(normalized)
	client, err := nativeapi.New(*server)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	key, err := credentialSource(*server, "WORKGRAPH_TOKEN", os.LookupEnv, loadNativeCredential, time.Now)(ctx)
	if err != nil {
		return err
	}
	response, err := client.Do(ctx, "POST", "/v1/context/search", raw, key)
	var responseError nativeapi.ResponseError
	if errors.As(err, &responseError) && responseError.Status == 409 && q.After != nil && q.After.SnapshotID != "" {
		return domain.ErrSearchExpired
	}
	if err != nil {
		return err
	}
	var result domain.Result
	if json.Unmarshal(response.Body, &result) != nil {
		return nativeapi.ErrProtocol
	}
	if result.SearchMode == "" {
		result.SearchMode = "lexical"
	}
	return json.NewEncoder(out).Encode(result)
}
