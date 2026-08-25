package operator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type Handler func(ctx context.Context, args []string, streams IO) error

type Command struct {
	Path    []string
	Aliases [][]string
	Summary string
	Usage   string
	Hidden  bool
	Handler Handler
}

type Registry struct {
	mu       sync.RWMutex
	commands map[string]Command
	aliases  map[string]string
}

func New() *Registry {
	return &Registry{commands: make(map[string]Command), aliases: make(map[string]string)}
}

func (registry *Registry) Register(command Command) error {
	if registry == nil {
		return errors.New("operator command registry is unavailable")
	}
	command.Path = normalizePath(command.Path)
	if len(command.Path) == 0 || command.Handler == nil || strings.TrimSpace(command.Summary) == "" {
		return errors.New("operator command path, summary, and handler are required")
	}
	key := pathKey(command.Path)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.commands[key]; exists || registry.aliases[key] != "" {
		return fmt.Errorf("operator command %q is already registered", strings.Join(command.Path, " "))
	}
	for index := range command.Aliases {
		command.Aliases[index] = normalizePath(command.Aliases[index])
		aliasKey := pathKey(command.Aliases[index])
		if aliasKey == "" {
			return errors.New("operator command alias is empty")
		}
		if _, exists := registry.commands[aliasKey]; exists || registry.aliases[aliasKey] != "" {
			return fmt.Errorf("operator command alias %q is already registered", strings.Join(command.Aliases[index], " "))
		}
	}
	registry.commands[key] = command
	for _, alias := range command.Aliases {
		registry.aliases[pathKey(alias)] = key
	}
	return nil
}

func (registry *Registry) Match(args []string) (Command, []string, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	for length := len(args); length > 0; length-- {
		key := pathKey(normalizePath(args[:length]))
		canonical := key
		if target := registry.aliases[key]; target != "" {
			canonical = target
		}
		if command, ok := registry.commands[canonical]; ok {
			return command, append([]string(nil), args[length:]...), true
		}
	}
	return Command{}, nil, false
}

func (registry *Registry) Run(ctx context.Context, args []string, streams IO) (bool, error) {
	command, remaining, ok := registry.Match(args)
	if !ok {
		return false, nil
	}
	if streams.In == nil || streams.Out == nil || streams.Err == nil {
		return true, errors.New("operator command streams are required")
	}
	return true, command.Handler(ctx, remaining, streams)
}

func (registry *Registry) Commands() []Command {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	commands := make([]Command, 0, len(registry.commands))
	for _, command := range registry.commands {
		command.Path = append([]string(nil), command.Path...)
		command.Aliases = cloneAliases(command.Aliases)
		commands = append(commands, command)
	}
	sort.Slice(commands, func(left, right int) bool { return pathKey(commands[left].Path) < pathKey(commands[right].Path) })
	return commands
}

func (registry *Registry) WriteHelp(output io.Writer, program string) error {
	if output == nil {
		return errors.New("operator help output is required")
	}
	program = strings.TrimSpace(program)
	if program == "" {
		program = "azem"
	}
	if _, err := fmt.Fprintf(output, "Usage: %s [command] [options] [prompt]\n\nCommands:\n", program); err != nil {
		return err
	}
	for _, command := range registry.Commands() {
		if command.Hidden {
			continue
		}
		name := strings.Join(command.Path, " ")
		if _, err := fmt.Fprintf(output, "  %-24s %s\n", name, command.Summary); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(output, "\nRun `%s <command> --help` for command details. Without a command, Azem starts the coding agent.\n", program)
	return err
}

func normalizePath(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\x00/\\") {
			return nil
		}
		result = append(result, value)
	}
	return result
}

func pathKey(values []string) string { return strings.Join(values, "\x00") }

func cloneAliases(values [][]string) [][]string {
	result := make([][]string, len(values))
	for index := range values {
		result[index] = append([]string(nil), values[index]...)
	}
	return result
}
