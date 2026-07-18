package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Source struct {
	Path    string
	Trusted bool
}
type Options struct {
	Sources        []Source
	DefaultTimeout time.Duration
	FailurePolicy  FailurePolicy
}
type Registry struct {
	commands    map[Event][]Command
	Diagnostics []Diagnostic
	claimed     map[string]bool
	mu          sync.Mutex
}

type fileConfig struct {
	Hooks map[Event][]group `json:"hooks"`
}
type group struct {
	Matcher string     `json:"matcher"`
	Hooks   []hookSpec `json:"hooks"`
}
type hookSpec struct {
	Name          string        `json:"name"`
	Type          string        `json:"type"`
	Command       string        `json:"command"`
	Args          []string      `json:"args"`
	If            string        `json:"if"`
	Shell         string        `json:"shell"`
	StatusMessage string        `json:"statusMessage"`
	Once          bool          `json:"once"`
	Async         bool          `json:"async"`
	AsyncRewake   bool          `json:"asyncRewake"`
	Timeout       float64       `json:"timeout"`
	FailurePolicy FailurePolicy `json:"failurePolicy"`
}

func (h *hookSpec) UnmarshalJSON(data []byte) error {
	type plain hookSpec
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	allowed := map[string]bool{"name": true, "type": true, "command": true, "args": true, "if": true, "shell": true,
		"statusMessage": true, "once": true, "async": true, "asyncRewake": true, "timeout": true, "failurePolicy": true}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unsupported command hook field %q", key)
		}
	}
	*h = hookSpec(decoded)
	return nil
}

func Discover(options Options) *Registry {
	r := &Registry{commands: make(map[Event][]Command), claimed: make(map[string]bool)}
	seen := map[string]bool{}
	for _, source := range options.Sources {
		if !source.Trusted {
			r.Diagnostics = append(r.Diagnostics, Diagnostic{Source: source.Path, Message: "source is not trusted; skipped"})
			continue
		}
		paths, err := sourceFiles(source.Path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				r.Diagnostics = append(r.Diagnostics, Diagnostic{Source: source.Path, Message: err.Error()})
			}
			continue
		}
		for _, path := range paths {
			r.load(path, options, seen)
		}
	}
	return r
}

func sourceFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		if e.Type().IsRegular() && filepath.Ext(name) == ".json" {
			paths = append(paths, filepath.Join(path, e.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (r *Registry) load(path string, options Options, seen map[string]bool) {
	f, err := os.Open(path)
	if err != nil {
		r.diag(path, "", err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		r.diag(path, "", fmt.Errorf("source is not a regular file"))
		return
	}
	if info.Size() > 1<<20 {
		r.diag(path, "", fmt.Errorf("source exceeds 1 MiB"))
		return
	}
	var cfg fileConfig
	d := json.NewDecoder(io.LimitReader(f, (1<<20)+1))
	if err := d.Decode(&cfg); err != nil {
		r.diag(path, "", fmt.Errorf("decode JSON: %w", err))
		return
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		if err == nil {
			r.diag(path, "", fmt.Errorf("multiple JSON documents are not allowed"))
		} else {
			r.diag(path, "", fmt.Errorf("decode trailing JSON: %w", err))
		}
		return
	}
	for event, groups := range cfg.Hooks {
		if !validEvents[event] {
			r.diag(path, event, fmt.Errorf("unknown event"))
			continue
		}
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h.Type != "command" {
					r.diag(path, event, fmt.Errorf("unsupported hook type %q", h.Type))
					continue
				}
				if h.Command == "" {
					r.diag(path, event, fmt.Errorf("command is empty"))
					continue
				}
				if h.AsyncRewake {
					r.diag(path, event, fmt.Errorf("asyncRewake is not supported by this runtime"))
					continue
				}
				timeout := options.DefaultTimeout
				if timeout <= 0 {
					timeout = 5 * time.Second
				}
				if h.Timeout > 0 {
					timeout = time.Duration(h.Timeout * float64(time.Second))
				}
				policy := h.FailurePolicy
				if policy == "" {
					policy = options.FailurePolicy
				}
				if policy == "" {
					policy = FailureOpen
				}
				if policy != FailureOpen && policy != FailureClosed {
					r.diag(path, event, fmt.Errorf("invalid failure policy"))
					continue
				}
				name := strings.TrimSpace(h.Name)
				if name == "" {
					name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
				}
				if h.Shell != "" && h.Shell != "bash" && h.Shell != "powershell" {
					r.diag(path, event, fmt.Errorf("invalid shell %q", h.Shell))
					continue
				}
				c := Command{Event: event, Name: name, Matcher: g.Matcher, If: h.If, RawCommand: h.Command, Args: append([]string(nil), h.Args...),
					Shell: h.Shell, StatusMessage: h.StatusMessage, Once: h.Once, Async: h.Async,
					Timeout: timeout, FailurePolicy: policy, Source: path}
				if err := compileMatcher(&c); err != nil {
					r.diag(path, event, err)
					continue
				}
				key := string(event) + "\x00command\x00" + h.Command + "\x00" + strings.Join(h.Args, "\x00") + "\x00" + g.Matcher + "\x00" + h.If + "\x00" + h.Shell + "\x00" + path
				if !seen[key] {
					seen[key] = true
					r.commands[event] = append(r.commands[event], c)
				}
			}
		}
	}
}
func (r *Registry) Claim(command Command) bool {
	if !command.Once {
		return true
	}
	key := string(command.Event) + "\x00" + command.Source + "\x00" + command.RawCommand + "\x00" + strings.Join(command.Args, "\x00") + "\x00" + command.Matcher + "\x00" + command.If + "\x00" + command.Shell
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimed[key] {
		return false
	}
	r.claimed[key] = true
	return true
}
func (r *Registry) diag(source string, event Event, err error) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{Source: source, Event: event, Message: err.Error()})
}
func (r *Registry) Commands(event Event) []Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Command(nil), r.commands[event]...)
}

func (r *Registry) Replace(next *Registry) {
	if r == nil || next == nil {
		return
	}
	r.mu.Lock()
	r.commands = next.commands
	r.Diagnostics = append([]Diagnostic(nil), next.Diagnostics...)
	r.mu.Unlock()
}
