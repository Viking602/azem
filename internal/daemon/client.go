package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/desktopipc"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

const (
	daemonStartTimeout = 20 * time.Second
	daemonProbeDelay   = 20 * time.Millisecond
)

type ClientOptions struct {
	Workspace    string
	ConfigFile   string
	DaemonBinary string
	StateDir     string
	ClientID     string
}

type InitialWorkspaceOptions struct {
	Workspace        string
	StartupDirectory string
	ConfigFile       string
}

func ConnectOrStart(ctx context.Context, options ClientOptions) (*desktopipc.Client, desktopipc.Endpoint, desktopipc.HelloAck, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	paths, err := config.ResolveClientPaths(strings.TrimSpace(options.Workspace), strings.TrimSpace(options.ConfigFile))
	if err != nil {
		return nil, desktopipc.Endpoint{}, desktopipc.HelloAck{}, err
	}
	stateDir := strings.TrimSpace(options.StateDir)
	if stateDir == "" {
		stateDir = paths.StateDir
	} else {
		stateDir, err = filepath.Abs(stateDir)
		if err != nil {
			return nil, desktopipc.Endpoint{}, desktopipc.HelloAck{}, fmt.Errorf("resolve daemon state directory: %w", err)
		}
		stateDir = filepath.Clean(stateDir)
	}
	endpointPath, err := EndpointPath(stateDir, paths.Workspace)
	if err != nil {
		return nil, desktopipc.Endpoint{}, desktopipc.HelloAck{}, err
	}
	if err := os.MkdirAll(filepath.Dir(endpointPath), 0o700); err != nil {
		return nil, desktopipc.Endpoint{}, desktopipc.HelloAck{}, fmt.Errorf("create daemon endpoint directory: %w", err)
	}
	deadline := time.Now().Add(daemonStartTimeout)
	var lastErr error
	for {
		client, endpoint, ack, probeErr := connectPublishedEndpoint(ctx, endpointPath, paths.Workspace, options.ClientID)
		if probeErr == nil {
			return client, endpoint, ack, nil
		}
		lastErr = probeErr
		lock, acquired, lockErr := tryAcquireStartLock(filepath.Join(filepath.Dir(endpointPath), "start.lock"))
		if lockErr != nil {
			return nil, desktopipc.Endpoint{}, desktopipc.HelloAck{}, lockErr
		}
		if acquired {
			client, endpoint, ack, probeErr = connectPublishedEndpoint(ctx, endpointPath, paths.Workspace, options.ClientID)
			if probeErr == nil {
				_ = lock.Close()
				return client, endpoint, ack, nil
			}
			lastErr = probeErr
			retired, retireErr := retireLegacyDaemon(ctx, endpointPath, paths.Workspace, options.ClientID, deadline)
			if retireErr != nil {
				_ = lock.Close()
				return nil, desktopipc.Endpoint{}, desktopipc.HelloAck{}, retireErr
			}
			if retired {
				lastErr = errors.New("pre-epoch daemon stopped for upgrade")
			}
			if err := spawnDaemon(options, paths.Workspace, paths.ConfigFile, stateDir, endpointPath); err != nil {
				_ = lock.Close()
				return nil, desktopipc.Endpoint{}, desktopipc.HelloAck{}, err
			}
			for time.Now().Before(deadline) {
				client, endpoint, ack, probeErr = connectPublishedEndpoint(ctx, endpointPath, paths.Workspace, options.ClientID)
				if probeErr == nil {
					_ = lock.Close()
					return client, endpoint, ack, nil
				}
				lastErr = probeErr
				if err := waitForDaemonProbe(ctx, deadline); err != nil {
					_ = lock.Close()
					return nil, desktopipc.Endpoint{}, desktopipc.HelloAck{}, err
				}
			}
			_ = lock.Close()
			return nil, desktopipc.Endpoint{}, desktopipc.HelloAck{}, fmt.Errorf("daemon did not publish a live endpoint within %s: %w", daemonStartTimeout, lastErr)
		}
		if err := waitForDaemonProbe(ctx, deadline); err != nil {
			return nil, desktopipc.Endpoint{}, desktopipc.HelloAck{}, err
		}
	}
}

func ResolveInitialWorkspace(ctx context.Context, options InitialWorkspaceOptions) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if explicit := strings.TrimSpace(options.Workspace); explicit != "" {
		paths, err := config.ResolveClientPaths(explicit, options.ConfigFile)
		if err != nil {
			return "", err
		}
		return paths.Workspace, nil
	}
	fallback := strings.TrimSpace(options.StartupDirectory)
	if fallback == "" {
		var err error
		fallback, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	paths, err := config.ResolveClientPaths(fallback, options.ConfigFile)
	if err != nil {
		return "", err
	}
	candidates, err := config.ClientDatabaseCandidates()
	if err != nil {
		return "", err
	}
	var recent string
	var recentAt int64
	for _, databasePath := range candidates {
		workspace, updatedAt, exists, err := recentVisibleWorkspace(ctx, databasePath)
		if err != nil {
			return "", err
		}
		if !exists {
			continue
		}
		canonical, canonicalErr := canonicalWorkspace(workspace)
		if canonicalErr != nil {
			continue
		}
		if recent == "" || updatedAt > recentAt {
			recent, recentAt = canonical, updatedAt
		}
	}
	if recent != "" {
		return recent, nil
	}
	if filepath.Dir(paths.Workspace) == paths.Workspace {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if canonical, err := canonicalWorkspace(home); err == nil {
			return canonical, nil
		}
	}
	return paths.Workspace, nil
}

func connectPublishedEndpoint(ctx context.Context, endpointPath, workspace, clientID string) (*desktopipc.Client, desktopipc.Endpoint, desktopipc.HelloAck, error) {
	endpoint, err := desktopipc.ReadEndpointFile(endpointPath)
	if err != nil {
		return nil, endpoint, desktopipc.HelloAck{}, fmt.Errorf("read daemon endpoint: %w", err)
	}
	workspaceID, err := desktopipc.WorkspaceID(workspace)
	if err != nil {
		return nil, endpoint, desktopipc.HelloAck{}, err
	}
	if endpoint.WorkspaceID != workspaceID {
		return nil, endpoint, desktopipc.HelloAck{}, errors.New("daemon endpoint belongs to another workspace")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, ack, err := desktopipc.Connect(probeCtx, endpoint, clientID, 0)
	if err != nil {
		return nil, endpoint, ack, fmt.Errorf("connect daemon endpoint: %w", err)
	}
	return client, endpoint, ack, nil
}

func retireLegacyDaemon(ctx context.Context, endpointPath, workspace, clientID string, deadline time.Time) (bool, error) {
	endpoint, err := desktopipc.ReadLegacyEndpointFile(endpointPath)
	if err != nil {
		return false, nil
	}
	workspaceID, err := desktopipc.WorkspaceID(workspace)
	if err != nil {
		return false, err
	}
	if endpoint.WorkspaceID != workspaceID {
		return false, errors.New("legacy daemon endpoint belongs to another workspace")
	}
	stopDeadline := deadline
	if candidate := time.Now().Add(5 * time.Second); candidate.Before(stopDeadline) {
		stopDeadline = candidate
	}
	stopCtx, cancel := context.WithDeadline(ctx, stopDeadline)
	defer cancel()
	if strings.TrimSpace(clientID) == "" {
		clientID = "azem-upgrade"
	}
	connected, stopErr := desktopipc.StopLegacyDaemonForUpgrade(stopCtx, endpoint, clientID)
	if !connected {
		if errors.Is(stopErr, desktopipc.ErrAuthentication) {
			return false, fmt.Errorf("authenticate pre-epoch Azem daemon pid %d for upgrade: %w", endpoint.PID, stopErr)
		}
		return false, nil
	}
	if stopErr != nil {
		var refusal *desktopipc.ProtocolRequestError
		if errors.As(stopErr, &refusal) && refusal.Code == "daemon_stop_refused" {
			return false, fmt.Errorf("existing Azem daemon pid %d must finish active work before this version can start: %w", endpoint.PID, stopErr)
		}
		return false, fmt.Errorf("stop legacy Azem daemon pid %d for upgrade: %w", endpoint.PID, stopErr)
	}
	for time.Now().Before(deadline) {
		if _, err := os.Stat(endpoint.TokenFile); errors.Is(err, os.ErrNotExist) {
			return true, nil
		} else if err != nil {
			return false, fmt.Errorf("wait for legacy daemon token removal: %w", err)
		}
		if err := waitForDaemonProbe(ctx, deadline); err != nil {
			return false, err
		}
	}
	return false, fmt.Errorf("legacy daemon pid %d did not stop before startup timeout", endpoint.PID)
}

func waitForDaemonProbe(ctx context.Context, deadline time.Time) error {
	if !time.Now().Before(deadline) {
		return fmt.Errorf("daemon did not publish an endpoint within %s", daemonStartTimeout)
	}
	timer := time.NewTimer(min(daemonProbeDelay, time.Until(deadline)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func spawnDaemon(options ClientOptions, workspace, configFile, stateDir, endpointPath string) error {
	binary, dedicated, err := resolveDaemonBinary(strings.TrimSpace(options.DaemonBinary))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(endpointPath), 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(filepath.Dir(endpointPath), "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()
	arguments := make([]string, 0, 7)
	if !dedicated {
		arguments = append(arguments, "daemon", "serve")
	}
	arguments = append(arguments, "--workspace", workspace)
	if strings.TrimSpace(configFile) != "" {
		arguments = append(arguments, "--config", configFile)
	}
	command := exec.Command(binary, arguments...)
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	command.Env = append(os.Environ(), config.HomeEnv+"="+stateDir)
	configureDetachedProcess(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Azem daemon with %s: %w", binary, err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("detach Azem daemon process: %w", err)
	}
	return nil
}

func resolveDaemonBinary(explicit string) (string, bool, error) {
	candidates := make([]string, 0, 3)
	if explicit != "" {
		candidates = append(candidates, explicit)
	} else if environment := strings.TrimSpace(os.Getenv("AZEM_DAEMON_BINARY")); environment != "" {
		candidates = append(candidates, environment)
	} else if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), daemonExecutableName()))
	}
	for _, candidate := range candidates {
		resolved, err := exec.LookPath(candidate)
		if err != nil {
			if explicit != "" || candidate == strings.TrimSpace(os.Getenv("AZEM_DAEMON_BINARY")) {
				return "", false, fmt.Errorf("resolve daemon binary %q: %w", candidate, err)
			}
			continue
		}
		return resolved, isDedicatedDaemonBinary(resolved), nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", false, fmt.Errorf("resolve current executable: %w", err)
	}
	return executable, false, nil
}

func daemonExecutableName() string {
	if runtime.GOOS == "windows" {
		return "azem-daemon.exe"
	}
	return "azem-daemon"
}

func isDedicatedDaemonBinary(path string) bool {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(path)), strings.ToLower(filepath.Ext(path)))
	return name == "azem-daemon"
}

func canonicalWorkspace(path string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %q is not a directory", resolved)
	}
	return filepath.Clean(resolved), nil
}

func recentVisibleWorkspace(ctx context.Context, databasePath string) (string, int64, bool, error) {
	if _, err := os.Stat(databasePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", 0, false, nil
		}
		return "", 0, false, err
	}
	fence, err := sqlitestore.AcquireCatalogReadFence(ctx, databasePath)
	if err != nil {
		return "", 0, false, fmt.Errorf("acquire catalog read fence for %s: %w", databasePath, err)
	}
	defer fence.Close()
	query := url.Values{}
	query.Set("mode", "ro")
	query.Add("_pragma", "query_only(1)")
	query.Add("_pragma", "busy_timeout(1000)")
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(databasePath), RawQuery: query.Encode()}).String()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", 0, false, fmt.Errorf("open project catalog %s: %w", databasePath, err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := database.PingContext(ctx); err != nil {
		return "", 0, false, fmt.Errorf("read project catalog %s: %w", databasePath, err)
	}
	projects, err := catalogTableColumns(ctx, database, "desktop_projects")
	if err != nil {
		return "", 0, false, fmt.Errorf("inspect project catalog %s: %w", databasePath, err)
	}
	if len(projects) == 0 {
		return "", 0, false, nil
	}
	workspaceState, err := catalogTableColumns(ctx, database, "workspace_session_state")
	if err != nil {
		return "", 0, false, fmt.Errorf("inspect workspace session catalog %s: %w", databasePath, err)
	}
	statement := "SELECT p.workspace, p.updated_at FROM desktop_projects p"
	if len(workspaceState) > 0 {
		statement = "SELECT p.workspace, COALESCE(w.updated_at,p.updated_at) FROM desktop_projects p LEFT JOIN workspace_session_state w ON w.anchor=p.workspace"
	}
	if projects["visible"] {
		statement += " WHERE p.visible=1"
	}
	statement += " ORDER BY 2 DESC,p.workspace LIMIT 1"
	var workspace string
	var updatedAt int64
	if err := database.QueryRowContext(ctx, statement).Scan(&workspace, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", 0, false, nil
		}
		return "", 0, false, fmt.Errorf("query project catalog %s: %w", databasePath, err)
	}
	return workspace, updatedAt, true, nil
}

func catalogTableColumns(ctx context.Context, database *sql.DB, table string) (map[string]bool, error) {
	var exists int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, nil
	}
	rows, err := database.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var sequence, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&sequence, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}
