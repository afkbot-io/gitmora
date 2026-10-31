// Command workgraph collects local agent context and integrates it with ordinary Git operations.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"workgraph.local/platform/internal/agent/codex"
	"workgraph.local/platform/internal/agent/opencode"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/collector/gitrepo"
	"workgraph.local/platform/internal/collector/httpclient"
	"workgraph.local/platform/internal/collector/sqlite"
	compatibility "workgraph.local/platform/internal/compatibility/domain"
	distribution "workgraph.local/platform/internal/distribution/domain"
	"workgraph.local/platform/internal/identity/native"
	"workgraph.local/platform/internal/jsonwire"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		code := 1
		message := "Gitmora operation failed"
		var hook *gitrepo.HookExit
		switch {
		case errors.Is(err, distribution.ErrInvalid):
			message = "Invalid client installation input or unsafe path; use self install --help"
		case errors.Is(err, distribution.ErrConflict):
			message = "Existing client preserved; inspect its SHA-256 and use --replace-sha256 for an explicit update"
		case errors.Is(err, distribution.ErrBusy):
			message = "Another client installation is active; retry unchanged after it finishes"
		case errors.Is(err, distribution.ErrUncertain):
			message = "Client replacement may have succeeded; rerun the same installer to verify installed bytes"
		case errors.Is(err, distribution.ErrIO):
			message = "Client installation I/O failed; verify the destination and retry unchanged"
		case errors.As(err, &hook):
			code = hook.Code
			message = "Existing Git hook rejected the operation"
		case errors.Is(err, compatibility.ErrUnsupported):
			message = "Unsupported import source format or target content; no complete import was recorded"
		case errors.Is(err, compatibility.ErrLimit):
			message = "Import exceeds bounded local acquisition/projection capacity; no complete import was recorded"
		case errors.Is(err, compatibility.ErrInvalid):
			message = "Invalid local import source, target association or arguments; use import --help"
		case errors.Is(err, domain.ErrInvalid):
			message = "Invalid collector input"
		case errors.Is(err, domain.ErrDisabled):
			message = "Collection is disabled; use enable or agent enable <agent> --local explicitly"
		case errors.Is(err, codex.ErrUnavailable):
			message = "Local Codex metadata API unavailable; verify the executable/version and retry agent capture codex"
		case errors.Is(err, codex.ErrProtocol):
			message = "Codex history is unsupported, changing or exceeds capture bounds; local source remains unchanged"
		case errors.Is(err, domain.ErrConflict):
			message = "Collector identity/configuration conflict; existing state was preserved"
		case errors.Is(err, domain.ErrNotInitialized):
			message = "Run workgraph init in this repository first"
		case errors.Is(err, native.ErrInvalid):
			message = "Invalid authentication input or saved credential"
		case errors.Is(err, native.ErrNotFound):
			message = "No saved credential; run workgraph auth login --server <URL>"
		case errors.Is(err, native.ErrVault):
			message = "System secret store unavailable; unlock Keychain or start Linux Secret Service"
		case errors.Is(err, native.ErrBusy):
			message = "Another authentication operation is active for this server"
		case errors.Is(err, native.ErrUnauthorized):
			message = "Saved credential is expired or revoked; run auth login explicitly"
		case errors.Is(err, native.ErrForbidden):
			message = "Credential operation was denied; saved recovery state was retained"
		case errors.Is(err, native.ErrUnavailable), errors.Is(err, native.ErrProtocol):
			message = "Authentication server unavailable or response invalid; saved recovery state was retained"
		case errors.Is(err, native.ErrConflict):
			message = "Authentication intent conflicts; resume unchanged or explicitly use --replace"
		case errors.Is(err, native.ErrRejected):
			message = "CLI login was rejected or cancelled; start a new explicit login if needed"
		case errors.Is(err, native.ErrEnrollmentGone):
			message = "CLI confirmation window unavailable; run auth logout before starting a new login"
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			message = interruptionMessage(os.Args[1:])
		}
		_, _ = fmt.Fprintln(os.Stderr, message)
		os.Exit(code)
	}
}

// interruptionMessage keeps cancellation recovery aligned with the interrupted command's durable state.
func interruptionMessage(args []string) string {
	if len(args) > 0 {
		switch args[0] {
		case "auth":
			return "Operation interrupted; pending authentication can be resumed with auth login"
		case "import":
			return "Import interrupted; retry the same immutable source and arguments to resume durable recording"
		case "self":
			return "Client installation interrupted; inspect installed bytes and retry the same installer"
		}
	}
	return "Operation interrupted; inspect saved state and retry the same operation"
}

func run(parent context.Context, args []string, input io.Reader, output, diagnostic io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		_, err := fmt.Fprintln(output, "Usage: gitmora init | capture | import | frontier | bind | checkpoint | sync | status | disable | enable | agent | auth | api | mcp | version | self install")
		return err
	}
	if args[0] == "version" || args[0] == "--version" {
		return clientVersion(args[1:], output)
	}
	if args[0] == "self" {
		return installClient(parent, args[1:], output)
	}
	if args[0] == "internal-hook" {
		return dispatchHook(parent, args[1:], input, output, diagnostic)
	}
	if args[0] == "internal-codex-hook" {
		return codexHook(parent, args[1:], input, output, diagnostic)
	}
	if args[0] == "agent" {
		return configureAgent(parent, args[1:], output)
	}
	if args[0] == "mcp" {
		return mcpCommand(parent, args[1:], input, output)
	}
	if args[0] == "api" {
		return apiCommand(parent, args[1:], input, output)
	}
	if args[0] == "auth" {
		return configureAuth(parent, args[1:], output, diagnostic)
	}
	if args[0] == "frontier" {
		return configureFrontier(parent, args[1:], input, output)
	}
	if args[0] == "import" {
		return configureImport(parent, args[1:], output, diagnostic)
	}
	command := args[0]
	switch command {
	case "init", "capture", "bind", "checkpoint", "sync", "status", "disable", "enable":
	default:
		return domain.ErrInvalid
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {
		_, _ = fmt.Fprintf(output, "Usage: gitmora %s [flags]\n", command)
		flags.SetOutput(output)
		flags.PrintDefaults()
		flags.SetOutput(io.Discard)
	}
	var server, repositoryID, remote, tokenEnv, countryID, taskID, headFrom string
	var clear, include, retry, watch, pinSource, captureSync, physical bool
	var frontiers frontierIDs
	if command == "capture" || command == "status" {
		flags.BoolVar(&physical, "physical-worktree", false, "Isolate native-source queries to the physical current worktree")
	}
	switch command {
	case "init":
		flags.StringVar(&server, "server", "", "Workgraph API base URL")
		flags.StringVar(&repositoryID, "repository-id", "", "Workgraph repository UUID")
		flags.StringVar(&remote, "remote", "origin", "Named Git push remote")
		flags.StringVar(&tokenEnv, "token-env", "WORKGRAPH_TOKEN", "Environment variable containing the scoped token")
	case "bind":
		flags.StringVar(&countryID, "country-id", "", "Tasktopia country UUID")
		flags.StringVar(&taskID, "task-id", "", "Tasktopia task UUID")
		flags.BoolVar(&clear, "clear", false, "Clear binding for new sessions")
	case "sync":
		flags.BoolVar(&include, "include-uncommitted", false, "Explicitly permit current uncommitted session delivery")
		flags.BoolVar(&retry, "retry", false, "Retry queued and blocked failures immediately")
		flags.BoolVar(&watch, "watch", false, "Retry eligible delivery every 15 seconds until interrupted")
	case "capture":
		flags.BoolVar(&pinSource, "pin-source", false, "Keep the native conversation's initial task binding")
		flags.StringVar(&headFrom, "observe-head-from", "", "Observe commits since a native tool's prior HEAD (or unborn)")
		flags.BoolVar(&captureSync, "sync", false, "Attempt bounded delivery of already eligible context after durable capture")
		flags.Var(&frontiers, "frontier", "Bind a completed durable native tool to this exact window; repeat for multiple tools")
	case "checkpoint", "status", "disable", "enable":
	default:
		return domain.ErrInvalid
	}
	parseErr := flags.Parse(args[1:])
	if errors.Is(parseErr, flag.ErrHelp) {
		return nil
	}
	if parseErr != nil || flags.NArg() != 0 {
		return domain.ErrInvalid
	}
	if headFrom != "" && (!pinSource || (headFrom != "unborn" && !capture.ValidCommit(headFrom))) {
		return domain.ErrInvalid
	}
	if len(frontiers) > 0 && (!pinSource || headFrom != "") {
		return domain.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	discover := gitrepo.Discover
	if physical {
		discover = gitrepo.DiscoverLocal
	}
	repository, err := discover(ctx, ".")
	if err != nil {
		return err
	}
	if command == "disable" {
		if e := repository.SetCollectionDisabled(true); e != nil {
			return e
		}
		if _, e := os.Stat(repository.LedgerPath()); e == nil {
			store, e := sqlite.Open(ctx, repository.LedgerPath())
			if e == nil {
				e = store.SetCollection(ctx, false, "")
				store.Close()
			}
			if e != nil {
				_, _ = fmt.Fprintln(diagnostic, "Collection disabled independently of unavailable ledger; existing queue preserved")
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		return repository.UninstallHooks(ctx)
	}
	disabled, err := repository.CollectionDisabled()
	if err != nil {
		return err
	}
	if disabled && command != "init" && command != "enable" && command != "status" {
		return domain.ErrDisabled
	}
	if command != "init" {
		if _, err := os.Stat(repository.LedgerPath()); os.IsNotExist(err) {
			return domain.ErrNotInitialized
		} else if err != nil {
			return err
		}
	}
	store, err := sqlite.Open(ctx, repository.LedgerPath())
	if err != nil {
		return err
	}
	defer store.Close()
	service := application.NewService(store, nil, time.Now)
	switch command {
	case "init":
		pushURL, err := repository.RemoteURL(ctx, remote)
		if err != nil {
			return err
		}
		config := domain.Config{Version: 1, RepositoryID: repositoryID, ServerURL: server, Remote: remote, RemoteURL: pushURL, TokenEnv: tokenEnv}
		if err := store.Configure(ctx, config); err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		if _, err := repository.InstallHooks(ctx, executable); err != nil {
			return err
		}
		if err := store.SetCollection(ctx, true, ""); err != nil {
			return err
		}
		if err := repository.SetCollectionDisabled(false); err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(map[string]string{"status": "enabled", "repository_id": repositoryID, "worktree": repository.WorktreeKey()})
	case "enable":
		executable, e := os.Executable()
		if e != nil {
			return e
		}
		if _, e = repository.InstallHooks(ctx, executable); e != nil {
			return e
		}
		if e = store.SetCollection(ctx, true, ""); e != nil {
			return e
		}
		if e = repository.SetCollectionDisabled(false); e != nil {
			return e
		}
		return json.NewEncoder(output).Encode(map[string]string{"status": "enabled"})

	case "capture":
		raw, err := readBounded(input)
		if err != nil {
			return err
		}
		var observation domain.Observation
		if jsonwire.DecodeObject(raw, &observation) != nil {
			return domain.ErrInvalid
		}
		record := service.Record
		if pinSource {
			record = service.RecordNative
		}
		var sessionID string
		if len(frontiers) > 0 {
			sessionID, err = service.RecordNativeFrontiers(ctx, repository.WorktreeKey(), observation, frontiers)
		} else {
			sessionID, err = record(ctx, repository.WorktreeKey(), observation)
		}
		if err != nil {
			return err
		}
		if headFrom != "" {
			before := headFrom
			if before == "unborn" {
				before = ""
			}
			after, err := repository.HeadID(ctx)
			if err != nil {
				return err
			}
			commits, err := repository.HeadWindow(ctx, before, after)
			if err != nil {
				return err
			}
			if err := service.AssociateSession(ctx, repository.WorktreeKey(), sessionID, commits); err != nil {
				return err
			}
		}
		reply := struct {
			SessionID string                  `json:"session_id"`
			Status    string                  `json:"status"`
			Delivery  *application.SyncResult `json:"delivery,omitempty"`
		}{SessionID: sessionID, Status: "recorded"}
		if captureSync {
			delivery, cancelDelivery := context.WithTimeout(ctx, 2*time.Second)
			result, err := drain(delivery, store)
			cancelDelivery()
			reply.Delivery = &result
			if err != nil || result.Failed > 0 {
				_, _ = fmt.Fprintln(diagnostic, "Context is recorded locally; delivery remains queued")
			}
		}
		return json.NewEncoder(output).Encode(reply)
	case "bind":
		if clear && (countryID != "" || taskID != "") {
			return domain.ErrInvalid
		}
		var task *capture.TaskRef
		if !clear {
			task = &capture.TaskRef{CountryID: countryID, TaskID: taskID}
		}
		return service.Bind(ctx, repository.WorktreeKey(), task)
	case "checkpoint":
		return service.Checkpoint(ctx, repository.WorktreeKey(), false)
	case "status":
		config, err := store.Config(ctx)
		if err != nil {
			return err
		}
		stats, err := store.Stats(ctx)
		if err != nil {
			return err
		}
		head, err := repository.HeadID(ctx)
		if err != nil {
			return err
		}
		pending, err := service.PendingFrontiers(ctx, repository.WorktreeKey())
		if err != nil {
			return err
		}
		missingEnd := 0
		for _, frontier := range pending {
			if frontier.After == nil {
				missingEnd++
			}
		}
		return json.NewEncoder(output).Encode(struct {
			RepositoryID      string            `json:"repository_id"`
			Worktree          string            `json:"worktree"`
			ServerURL         string            `json:"server_url"`
			Queue             application.Stats `json:"queue"`
			Root              string            `json:"worktree_root"`
			Head              string            `json:"head"`
			CollectionEnabled bool              `json:"collection_enabled"`
			DisabledAgents    []string          `json:"disabled_agents"`
			PendingTools      int               `json:"pending_tools"`
			ToolsMissingEnd   int               `json:"tools_missing_end"`
		}{config.RepositoryID, repository.WorktreeKey(), config.ServerURL, stats, repository.Root, head, !config.Disabled && !disabled, config.DisabledAgents, len(pending), missingEnd})
	case "sync":
		if include {
			if err := service.Checkpoint(ctx, repository.WorktreeKey(), true); err != nil {
				return err
			}
		}
		if retry {
			if err := store.Retry(ctx); err != nil {
				return err
			}
		}
		if !watch {
			return synchronize(ctx, store, output)
		}
		// Watch performs bounded batches; no 30-second parent deadline is reused across iterations.
		for {
			batch, batchCancel := context.WithTimeout(parent, 30*time.Second)
			err := synchronize(batch, store, output)
			batchCancel()
			if parent.Err() != nil {
				return nil
			}
			if err != nil {
				if errors.Is(err, domain.ErrDisabled) {
					return err
				}
				_, _ = fmt.Fprintln(diagnostic, "Context remains queued; retry scheduled")
			}
			timer := time.NewTimer(15 * time.Second)
			select {
			case <-parent.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	default:
		return domain.ErrInvalid
	}
}

func configureAgent(parent context.Context, args []string, output io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		_, err := fmt.Fprintln(output, "Usage: gitmora agent enable|disable opencode|codex [--local | --config-dir /absolute/path] [--codex /absolute/executable]\nGlobal installation preserves foreign configuration. Codex hooks require native /hooks review. --local changes capture/delivery consent for this repository.\nRecovery: gitmora agent capture codex --session <thread-ID> [--codex /absolute/executable]")
		return err
	}
	if len(args) >= 2 && args[0] == "capture" && args[1] == "codex" {
		return codexCapture(parent, args[2:], output)
	}
	if len(args) < 2 || (args[1] != "opencode" && args[1] != "codex") || (args[0] != "enable" && args[0] != "disable") {
		return domain.ErrInvalid
	}
	flags := flag.NewFlagSet("agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("config-dir", "", "Agent configuration directory")
	codexPath := flags.String("codex", "", "Codex executable (default: PATH)")
	local := flags.Bool("local", false, "Change collection consent only for this repository")
	if flags.Parse(args[2:]) != nil || flags.NArg() != 0 {
		return domain.ErrInvalid
	}
	if *local {
		if *directory != "" || *codexPath != "" {
			return domain.ErrInvalid
		}
		repo, e := gitrepo.Discover(parent, ".")
		if e != nil {
			return e
		}
		if _, e = os.Stat(repo.LedgerPath()); e != nil {
			return domain.ErrNotInitialized
		}
		store, e := sqlite.Open(parent, repo.LedgerPath())
		if e != nil {
			return e
		}
		defer store.Close()
		if e = store.SetCollection(parent, args[0] == "enable", args[1]); e != nil {
			return e
		}
		return json.NewEncoder(output).Encode(map[string]any{"agent": args[1], "collection_enabled": args[0] == "enable", "scope": "repository"})
	}
	if args[1] == "codex" {
		if *directory == "" {
			value, err := codex.DefaultDirectory()
			if err != nil {
				return err
			}
			*directory = value
		}
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		if args[0] == "disable" {
			return codex.Disable(ctx, *directory)
		}
		selected, err := codexExecutable(*codexPath)
		if err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		if err = codex.Enable(ctx, *directory, executable, selected); err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(map[string]any{"agent": "codex", "status": "installed", "config_dir": *directory, "trust_required": true, "next": "Open /hooks in Codex and review the Gitmora commands; restart or resume the session afterwards"})
	}
	if *codexPath != "" {
		return domain.ErrInvalid
	}

	if *directory == "" {
		value, err := opencode.DefaultDirectory()
		if err != nil {
			return err
		}
		*directory = value
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if args[0] == "disable" {
		return opencode.Disable(ctx, *directory)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if err := opencode.Enable(ctx, *directory, executable); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]string{"agent": "opencode", "status": "enabled", "config_dir": *directory})
}

func synchronize(ctx context.Context, store *sqlite.Store, output io.Writer) error {
	total, err := drain(ctx, store)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(total); err != nil {
		return err
	}
	if total.Failed > 0 {
		return errors.New("delivery remains queued")
	}
	return nil
}

func drain(ctx context.Context, store *sqlite.Store) (application.SyncResult, error) {
	total := application.SyncResult{}
	config, err := store.Config(ctx)
	if err != nil {
		return total, err
	}
	uploader, err := httpclient.New(config, collectorTokenSource(config, os.LookupEnv, loadNativeCredential, time.Now))
	if err != nil {
		return total, err
	}
	service := application.NewService(store, uploader, time.Now)
	for {
		result, err := service.Sync(ctx)
		total.Attempted += result.Attempted
		total.Sent += result.Sent
		total.Failed += result.Failed
		total.Stats = result.Stats
		if err != nil {
			return total, err
		}
		if result.Attempted < 16 {
			break
		}
	}
	return total, nil
}

func dispatchHook(parent context.Context, args []string, input io.Reader, output, diagnostic io.Writer) error {
	flags := flag.NewFlagSet("internal-hook", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("installation", "", "Owned hook manifest")
	name := flags.String("name", "", "Git hook name")
	if err := flags.Parse(args); err != nil {
		return domain.ErrInvalid
	}
	if *name != "post-commit" && *name != "pre-push" && *name != "post-rewrite" {
		return domain.ErrInvalid
	}
	installation, err := gitrepo.ReadInstallation(*path)
	if err != nil {
		return err
	}
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	if *name == "post-commit" {
		if err := collectHook(parent, *name, flags.Args(), nil); err != nil {
			_, _ = fmt.Fprintln(diagnostic, "Workgraph context capture needs attention; Git commit is preserved")
		}
		return gitrepo.RunOriginal(parent, installation, directory, *name, flags.Args(), input, output, diagnostic)
	}
	raw, readErr := readBounded(input)
	if readErr != nil {
		// Replay even an oversized stream intact to the user hook; only collector processing is skipped.
		if err := gitrepo.RunOriginal(parent, installation, directory, *name, flags.Args(), io.MultiReader(bytes.NewReader(raw), input), output, diagnostic); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(diagnostic, "Workgraph skipped oversized hook input; use manual synchronization")
		return nil
	}
	if *name == "pre-push" {
		if err := gitrepo.RunOriginal(parent, installation, directory, *name, flags.Args(), bytes.NewReader(raw), output, diagnostic); err != nil {
			return err
		}
		if err := collectHook(parent, *name, flags.Args(), raw); err != nil {
			_, _ = fmt.Fprintln(diagnostic, "Workgraph context remains local; code push can continue")
		}
		return nil
	}
	if err := collectHook(parent, *name, flags.Args(), raw); err != nil {
		_, _ = fmt.Fprintln(diagnostic, "Workgraph rewrite context needs attention; Git history is preserved")
	}
	return gitrepo.RunOriginal(parent, installation, directory, *name, flags.Args(), bytes.NewReader(raw), output, diagnostic)
}

func collectHook(parent context.Context, name string, args []string, raw []byte) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	repository, err := gitrepo.Discover(ctx, ".")
	if err != nil {
		return err
	}
	if disabled, e := repository.CollectionDisabled(); e != nil || disabled {
		return e
	}
	store, err := sqlite.Open(ctx, repository.LedgerPath())
	if err != nil {
		return err
	}
	defer store.Close()
	config, err := store.Config(ctx)
	if err != nil {
		return err
	}
	if config.Disabled {
		return nil
	}
	service := application.NewService(store, nil, time.Now)
	switch name {
	case "post-commit":
		info, err := repository.Head(ctx)
		if err != nil {
			return err
		}
		return service.Commit(ctx, repository.WorktreeKey(), info.SHA, info.Changed, info.Dirty)
	case "post-rewrite":
		mapping := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 2 {
				return domain.ErrInvalid
			}
			if existing, ok := mapping[fields[0]]; ok && existing != fields[1] {
				return domain.ErrConflict
			}
			mapping[fields[0]] = fields[1]
		}
		return service.Rewrite(ctx, mapping)
	case "pre-push":
		if len(args) != 2 {
			return domain.ErrInvalid
		}
		if args[0] != config.Remote || args[1] != config.RemoteURL {
			return nil
		}
		actualURL, err := repository.RemoteURL(ctx, config.Remote)
		if err != nil {
			return err
		}
		if actualURL != config.RemoteURL {
			return domain.ErrConflict
		}
		commits, err := repository.Reachable(ctx, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		if err := service.PreparePush(ctx, commits); err != nil {
			return err
		}
		return synchronize(ctx, store, io.Discard)
	default:
		return domain.ErrInvalid
	}
}

func readBounded(input io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(input, 3*1024*1024+1))
	if err != nil || len(raw) > 3*1024*1024 {
		return raw, domain.ErrInvalid
	}
	return raw, nil
}
