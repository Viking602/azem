package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/blobstore"
	"github.com/Viking602/azem/internal/config"
	evalpkg "github.com/Viking602/azem/internal/eval"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

var (
	version   = "dev"
	gitCommit = "unknown"
	buildTime = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "azem-eval: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("azem-eval", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		workspace         string
		configFile        string
		prompt            string
		promptFile        string
		provider          string
		model             string
		reasoning         string
		timeout           time.Duration
		printEvents       bool
		showVersion       bool
		exportAuthFrom    string
		exportAuthTo      string
		refreshAuthDB     string
		syncAuthFrom      string
		syncAuthTo        string
		trajectoryDB      string
		trajectoryBlobs   string
		trajectorySession string
		trajectoryOut     string
		baselineOut       string
	)
	fs.StringVar(&workspace, "workspace", "", "workspace root (default: current directory)")
	fs.StringVar(&configFile, "config", "", "eval config.yaml (written with YOLO defaults if missing)")
	fs.StringVar(&prompt, "prompt", "", "task instruction")
	fs.StringVar(&promptFile, "prompt-file", "", "file containing the task instruction")
	fs.StringVar(&provider, "provider", envOr("AZEM_EVAL_PROVIDER", "chatgpt"), "model provider")
	fs.StringVar(&model, "model", envOr("AZEM_EVAL_MODEL", "gpt-5.6-sol"), "model id")
	fs.StringVar(&reasoning, "reasoning", envOr("AZEM_EVAL_REASONING", "high"), "reasoning effort")
	fs.DurationVar(&timeout, "timeout", 45*time.Minute, "maximum wall clock for the turn")
	fs.BoolVar(&printEvents, "print-events", false, "print runtime events as they arrive")
	fs.BoolVar(&showVersion, "version", false, "print version")
	fs.StringVar(&exportAuthFrom, "export-auth-from", "", "copy accounts and credentials from this Azem database")
	fs.StringVar(&exportAuthTo, "export-auth-to", "", "write a slim auth-only database")
	fs.StringVar(&refreshAuthDB, "refresh-auth", "", "refresh active Grok/ChatGPT tokens in this database")
	fs.StringVar(&syncAuthFrom, "sync-auth-from", "", "copy newer Grok/ChatGPT credentials from this database")
	fs.StringVar(&syncAuthTo, "sync-auth-to", "", "write newer Grok/ChatGPT credentials into this database")
	fs.StringVar(&trajectoryDB, "export-trajectory-db", "", "read durable session records from this database")
	fs.StringVar(&trajectoryBlobs, "export-trajectory-blobs", "", "blob directory (default: <database-dir>/blobs)")
	fs.StringVar(&trajectorySession, "export-trajectory-session", "", "session id to export")
	fs.StringVar(&trajectoryOut, "export-trajectory-out", "-", "trajectory JSON path or - for stdout")
	fs.StringVar(&baselineOut, "baseline-out", "", "write reproducible run baseline JSON before execution")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(os.Stdout)
			fmt.Fprintln(os.Stdout, "azem-eval runs one unattended coding turn for Harbor / Terminal-Bench.")
			fs.PrintDefaults()
			return nil
		}
		return err
	}
	if showVersion {
		fmt.Fprintf(os.Stdout, "azem-eval %s (%s %s)\n", version, gitCommit, buildTime)
		return nil
	}
	if trajectoryDB != "" || trajectorySession != "" || trajectoryBlobs != "" {
		if trajectoryDB == "" || trajectorySession == "" {
			return fmt.Errorf("export-trajectory-db and export-trajectory-session must be set together")
		}
		return exportTrajectory(trajectoryDB, trajectoryBlobs, trajectorySession, trajectoryOut)
	}
	if exportAuthFrom != "" || exportAuthTo != "" {
		if exportAuthFrom == "" || exportAuthTo == "" {
			return fmt.Errorf("export-auth-from and export-auth-to must be set together")
		}
		return exportAuth(exportAuthFrom, exportAuthTo)
	}
	if refreshAuthDB != "" {
		if err := refreshAuth(refreshAuthDB); err != nil {
			return err
		}
		if syncAuthFrom == "" && syncAuthTo == "" {
			return nil
		}
	}
	if syncAuthFrom != "" || syncAuthTo != "" {
		if syncAuthFrom == "" || syncAuthTo == "" {
			return fmt.Errorf("sync-auth-from and sync-auth-to must be set together")
		}
		return syncAuth(syncAuthFrom, syncAuthTo)
	}
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		workspace = cwd
	}
	applyEvalTLS()
	text, err := loadPrompt(prompt, promptFile)
	if err != nil {
		return err
	}
	if configFile == "" {
		configFile = filepath.Join(os.TempDir(), "azem-eval-config.yaml")
	}
	if err := writeEvalConfig(configFile, workspace, provider, model, reasoning); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if timeout > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, timeout)
		defer timeoutCancel()
	}
	boot, err := app.BootstrapAtWorkspace(ctx, workspace, configFile)
	if err != nil {
		return err
	}
	boot.Service.SetDesktopSurface(false)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer shutdownCancel()
		_ = boot.Service.Shutdown(shutdownCtx)
	}()
	if baselineOut != "" {
		identity, captureErr := evalpkg.CaptureBaseline(ctx, evalpkg.BaselineOptions{
			Workspace: workspace, AzemVersion: version, AzemCommit: gitCommit, BuildTime: buildTime,
			Provider: provider, Model: model, Reasoning: reasoning,
			InstructionFingerprint: app.InstructionFingerprint(false), TaskPrompt: text,
			Tools: boot.Service.ToolDefinitionsSnapshot(),
		})
		if captureErr != nil {
			return captureErr
		}
		if err := writeBaselineOutput(baselineOut, identity); err != nil {
			return err
		}
	}
	runID, err := boot.Service.StartConfiguredTurn(app.TurnRequest{
		Prompt:    text,
		Provider:  provider,
		Model:     model,
		Reasoning: reasoning,
		AgentMode: "single",
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "azem-eval: started run %s in %s\n", runID, workspace)
	return drainTurn(ctx, boot.Service, runID, printEvents)
}

func loadPrompt(prompt, promptFile string) (string, error) {
	if prompt != "" && promptFile != "" {
		return "", fmt.Errorf("set only one of --prompt or --prompt-file")
	}
	if promptFile != "" {
		data, err := os.ReadFile(promptFile)
		if err != nil {
			return "", err
		}
		prompt = string(data)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is empty")
	}
	return evalInstructionPrefix + prompt, nil
}

func writeEvalConfig(path, workspace, provider, model, reasoning string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body := fmt.Sprintf(`version: 1
defaults:
  provider: %q
  model: %q
  reasoning: %q
  agent_mode: single
  approval_mode: yolo
  queue_mode: queue
workspace:
  root: %q
  allow_write: true
  shell_policy: allow
  allow_network: allow
auth:
  store: sqlite
  import_codex: false
  import_grok: false
plugins:
  enabled: false
  import_codex: false
hooks:
  enabled: false
  default_timeout: 5s
  failure_policy: open
`, provider, model, reasoning, workspace)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return err
	}
	if _, err := config.LoadAtWorkspace(path, workspace); err != nil {
		return fmt.Errorf("eval config: %w", err)
	}
	return nil
}

func drainTurn(ctx context.Context, service *app.Service, runID string, printEvents bool) error {
	for {
		event, err := service.NextEvent(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				service.CancelActiveWithChildren(true)
				return fmt.Errorf("turn timed out")
			}
			return err
		}
		if event.RunID != "" && event.RunID != runID {
			continue
		}
		if printEvents {
			fmt.Fprintf(os.Stderr, "%s %s %s\n", event.Kind, event.State, truncate(event.Text, 160))
		}
		switch event.Kind {
		case app.EventApprovalRequested:
			return fmt.Errorf("approval requested under YOLO: %s", event.ApprovalID)
		case app.EventUserInputRequested:
			return fmt.Errorf("user input requested: %s", event.UserInputID)
		case app.EventPlanProposed:
			return fmt.Errorf("plan proposed; eval turns must execute directly")
		case app.EventRunFailed:
			if event.Text == "" {
				return fmt.Errorf("run failed")
			}
			return fmt.Errorf("run failed: %s", event.Text)
		case app.EventRunCancelled:
			return fmt.Errorf("run cancelled")
		case app.EventRunFinished:
			fmt.Fprintln(os.Stderr, "azem-eval: run finished")
			return nil
		}
	}
}

func writeBaselineOutput(outputPath string, identity evalpkg.BaselineIdentityV1) error {
	if outputPath == "-" {
		return evalpkg.WriteBaseline(os.Stdout, identity)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return fmt.Errorf("create baseline output directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(outputPath), ".azem-baseline-*.tmp")
	if err != nil {
		return fmt.Errorf("create baseline output: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("write baseline output: chmod: %w", err)
	}
	if err := evalpkg.WriteBaseline(file, identity); err != nil {
		_ = file.Close()
		return fmt.Errorf("write baseline output: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("write baseline output: sync: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("write baseline output: close: %w", err)
	}
	info, err := os.Stat(tempPath)
	if err != nil {
		return fmt.Errorf("write baseline output: stat: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("write baseline output: empty temporary file")
	}
	encoded, err := os.ReadFile(tempPath)
	if err != nil {
		return fmt.Errorf("write baseline output: verify: %w", err)
	}
	if _, err := evalpkg.ReadBaseline(strings.NewReader(string(encoded))); err != nil {
		return fmt.Errorf("write baseline output: verify: %w", err)
	}
	if err := os.Rename(tempPath, outputPath); err != nil {
		return fmt.Errorf("install baseline output: %w", err)
	}
	if err := syncOutputDirectory(filepath.Dir(outputPath)); err != nil {
		return fmt.Errorf("install baseline output: sync directory: %w", err)
	}
	return nil
}

func syncOutputDirectory(directory string) error {
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}

func readOnlySQLiteDSN(databasePath string) (string, error) {
	absolute, err := filepath.Abs(databasePath)
	if err != nil {
		return "", fmt.Errorf("resolve trajectory database path: %w", err)
	}
	path := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("mode", "ro")
	query.Set("_pragma", "query_only(1)")
	uri.RawQuery = query.Encode()
	return uri.String(), nil
}

func exportTrajectory(databasePath, blobRoot, sessionID, outputPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	source, err := openTrajectoryDatabase(ctx, databasePath)
	if err != nil {
		return err
	}
	defer source.Close()
	blobs, err := openTrajectoryBlobs(databasePath, blobRoot)
	if err != nil {
		return err
	}
	trajectory, err := evalpkg.ExportTrajectory(ctx, source, blobs, sessionID)
	if err != nil {
		return err
	}
	return writeTrajectoryOutput(outputPath, trajectory)
}

func openTrajectoryDatabase(ctx context.Context, databasePath string) (*sql.DB, error) {
	dsn, err := readOnlySQLiteDSN(databasePath)
	if err != nil {
		return nil, err
	}
	source, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open trajectory database: %w", err)
	}
	if err := source.PingContext(ctx); err != nil {
		source.Close()
		return nil, fmt.Errorf("read trajectory database: %w", err)
	}
	return source, nil
}

func openTrajectoryBlobs(databasePath, blobRoot string) (blobstore.Store, error) {
	if blobRoot == "" {
		blobRoot = filepath.Join(filepath.Dir(databasePath), "blobs")
	}
	if _, err := os.Stat(blobRoot); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect trajectory blob directory: %w", err)
	}
	return blobstore.NewDirectory(blobRoot)
}

func writeTrajectoryOutput(outputPath string, trajectory evalpkg.TrajectoryV1) error {
	if outputPath == "" || outputPath == "-" {
		return evalpkg.WriteTrajectory(os.Stdout, trajectory)
	}
	return writeTrajectoryFile(outputPath, trajectory)
}

func writeTrajectoryFile(outputPath string, trajectory evalpkg.TrajectoryV1) error {
	directory := filepath.Dir(outputPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create trajectory output directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".azem-trajectory-*.tmp")
	if err != nil {
		return fmt.Errorf("create trajectory output: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := writeTrajectoryTemp(file, trajectory); err != nil {
		return err
	}
	if err := verifyTrajectoryTemp(tempPath); err != nil {
		return err
	}
	return installTrajectoryOutput(tempPath, outputPath)
}

func writeTrajectoryTemp(file *os.File, trajectory evalpkg.TrajectoryV1) error {
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("write trajectory output: chmod: %w", err)
	}
	if err := evalpkg.WriteTrajectory(file, trajectory); err != nil {
		file.Close()
		return fmt.Errorf("write trajectory output: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("write trajectory output: sync: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("write trajectory output: close: %w", err)
	}
	return nil
}

func verifyTrajectoryTemp(tempPath string) error {
	info, err := os.Stat(tempPath)
	if err != nil {
		return fmt.Errorf("write trajectory output: stat: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("write trajectory output: empty temporary file")
	}
	encoded, err := os.ReadFile(tempPath)
	if err != nil {
		return fmt.Errorf("write trajectory output: verify: %w", err)
	}
	if _, err := evalpkg.ReadTrajectory(bytes.NewReader(encoded)); err != nil {
		return fmt.Errorf("write trajectory output: verify: %w", err)
	}
	return nil
}

func installTrajectoryOutput(tempPath, outputPath string) error {
	if err := os.Rename(tempPath, outputPath); err != nil {
		return fmt.Errorf("install trajectory output: %w", err)
	}
	if err := syncOutputDirectory(filepath.Dir(outputPath)); err != nil {
		return fmt.Errorf("install trajectory output: sync directory: %w", err)
	}
	return nil
}

func exportAuth(from, to string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	src, err := sql.Open("sqlite", "file:"+filepath.ToSlash(from)+"?mode=ro")
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	_ = os.Remove(to)
	dest, err := sqlitestore.Open(ctx, to)
	if err != nil {
		return err
	}
	defer dest.Close(ctx)
	if err := copyTable(ctx, src, dest.DB(), "accounts", []string{
		"id", "provider_id", "email", "display_name", "plan", "credential_ref", "status", "created_at", "updated_at",
	}); err != nil {
		return err
	}
	if err := copyTable(ctx, src, dest.DB(), "auth_credentials", []string{
		"provider_id", "account_id", "data", "created_at", "updated_at",
	}); err != nil {
		return err
	}
	return nil
}

func copyTable(ctx context.Context, src *sql.DB, dest *sql.DB, table string, columns []string) error {
	query := "SELECT " + strings.Join(columns, ", ") + " FROM " + table
	rows, err := src.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read %s: %w", table, err)
	}
	defer rows.Close()
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
	insert := "INSERT OR REPLACE INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + placeholders + ")"
	for rows.Next() {
		values := make([]any, len(columns))
		dests := make([]any, len(columns))
		for i := range values {
			dests[i] = &values[i]
		}
		if err := rows.Scan(dests...); err != nil {
			return err
		}
		if _, err := dest.ExecContext(ctx, insert, values...); err != nil {
			return fmt.Errorf("write %s: %w", table, err)
		}
	}
	return rows.Err()
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func truncate(text string, limit int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

const evalInstructionPrefix = `You are solving an unattended Terminal-Bench task in this workspace.
Complete the instruction fully. Inspect the workspace, edit files, and run
commands as needed. Do not modify hidden tests or the verifier unless the
instruction explicitly requires it. Do not ask the user questions. Do not
propose a plan and wait. Finish the deliverable.

Task:

`
