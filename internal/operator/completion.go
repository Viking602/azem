package operator

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

type Shell string

const (
	ShellBash Shell = "bash"
	ShellZsh  Shell = "zsh"
	ShellFish Shell = "fish"
)

type CompletionOptions struct {
	Program     string
	GlobalFlags []string
}

func WriteCompletion(output io.Writer, shell Shell, registry *Registry, options CompletionOptions) error {
	if output == nil || registry == nil {
		return errors.New("completion output and command registry are required")
	}
	program := strings.TrimSpace(options.Program)
	if program == "" {
		program = "azem"
	}
	commands := registry.Commands()
	flags := normalizedFlags(options.GlobalFlags)
	switch shell {
	case ShellBash:
		return writeBashCompletion(output, program, commands, flags)
	case ShellZsh:
		return writeZshCompletion(output, program, commands, flags)
	case ShellFish:
		return writeFishCompletion(output, program, commands, flags)
	default:
		return fmt.Errorf("unsupported completion shell %q", shell)
	}
}

func writeBashCompletion(output io.Writer, program string, commands []Command, flags []string) error {
	roots, children := completionTree(commands)
	if _, err := fmt.Fprintf(output, `# bash completion for %[1]s
_%[1]s_completion() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  if (( COMP_CWORD == 1 )); then
    COMPREPLY=( $(compgen -W '%[2]s %[3]s' -- "$cur") )
    return
  fi
  case "${COMP_WORDS[1]}" in
`, program, strings.Join(roots, " "), strings.Join(flags, " ")); err != nil {
		return err
	}
	for _, root := range roots {
		values := children[root]
		if len(values) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(output, "    %s) COMPREPLY=( $(compgen -W '%s' -- \"$cur\") ) ;;\n", root, strings.Join(values, " ")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(output, `  esac
}
complete -F _%[1]s_completion %[1]s
`, program)
	return err
}

func writeZshCompletion(output io.Writer, program string, commands []Command, flags []string) error {
	roots, children := completionTree(commands)
	if _, err := fmt.Fprintf(output, "#compdef %s\n\n_%s() {\n  local -a roots\n  roots=(%s)\n  if (( CURRENT == 2 )); then\n    _describe 'command' roots\n    _arguments '%s'\n    return\n  fi\n  case $words[2] in\n", program, program, quoteZsh(roots), strings.Join(flags, "' '")); err != nil {
		return err
	}
	for _, root := range roots {
		values := children[root]
		if len(values) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(output, "    %s) local -a subcommands; subcommands=(%s); _describe 'subcommand' subcommands ;;\n", root, quoteZsh(values)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(output, "  esac\n}\n\ncompdef _%s %s\n", program, program)
	return err
}

func writeFishCompletion(output io.Writer, program string, commands []Command, flags []string) error {
	if _, err := fmt.Fprintf(output, "# fish completion for %s\ncomplete -c %s -f\n", program, program); err != nil {
		return err
	}
	roots, children := completionTree(commands)
	for _, root := range roots {
		if _, err := fmt.Fprintf(output, "complete -c %s -n '__fish_use_subcommand' -a %s\n", program, shellQuote(root)); err != nil {
			return err
		}
		for _, child := range children[root] {
			if _, err := fmt.Fprintf(output, "complete -c %s -n '__fish_seen_subcommand_from %s' -a %s\n", program, shellQuote(root), shellQuote(child)); err != nil {
				return err
			}
		}
	}
	for _, flag := range flags {
		name := strings.TrimPrefix(flag, "--")
		if strings.HasPrefix(flag, "--") {
			if _, err := fmt.Fprintf(output, "complete -c %s -l %s\n", program, shellQuote(name)); err != nil {
				return err
			}
		} else if strings.HasPrefix(flag, "-") && len(flag) == 2 {
			if _, err := fmt.Fprintf(output, "complete -c %s -s %s\n", program, shellQuote(strings.TrimPrefix(flag, "-"))); err != nil {
				return err
			}
		}
	}
	return nil
}

func completionTree(commands []Command) ([]string, map[string][]string) {
	rootSet := make(map[string]struct{})
	childrenSet := make(map[string]map[string]struct{})
	for _, command := range commands {
		if command.Hidden || len(command.Path) == 0 {
			continue
		}
		root := command.Path[0]
		rootSet[root] = struct{}{}
		if len(command.Path) > 1 {
			if childrenSet[root] == nil {
				childrenSet[root] = make(map[string]struct{})
			}
			childrenSet[root][command.Path[1]] = struct{}{}
		}
	}
	roots := setValues(rootSet)
	children := make(map[string][]string, len(childrenSet))
	for root, values := range childrenSet {
		children[root] = setValues(values)
	}
	return roots, children
}

func normalizedFlags(values []string) []string {
	set := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "-") && !strings.ContainsAny(value, " \t\r\n'\"") {
			set[value] = struct{}{}
		}
	}
	return setValues(set)
}

func setValues(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func quoteZsh(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = shellQuote(value)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
