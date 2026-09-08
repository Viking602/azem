package app

import (
	"fmt"
	"strings"
)

type ExpandedSkillInvocation struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

func (s *Service) ExpandSkillInvocation(name, arguments string) (ExpandedSkillInvocation, error) {
	name = strings.TrimSpace(name)
	if name == "" || s == nil || s.coding == nil {
		return ExpandedSkillInvocation{}, fmt.Errorf("skill %q is unavailable", name)
	}
	snapshot := s.coding.SkillSnapshot()
	if snapshot.Registry == nil {
		return ExpandedSkillInvocation{}, fmt.Errorf("skill %q is unavailable", name)
	}
	resolved, err := snapshot.Registry.Resolve(name)
	if err != nil {
		return ExpandedSkillInvocation{}, err
	}
	if len(resolved) != 1 || strings.TrimSpace(resolved[0].Body) == "" {
		return ExpandedSkillInvocation{}, fmt.Errorf("skill %q is unavailable", name)
	}
	skill := resolved[0]
	prompt := fmt.Sprintf("[IMPORTANT: The user has invoked the %q skill, indicating they want you to follow its instructions. The full skill content is loaded below.]\n\n%s\n\n---\n\n[Skill directory: %s]\nResolve any relative paths in this skill against that directory.", skill.Name, skill.Body, skill.SourceDir)
	if arguments = strings.TrimSpace(arguments); arguments != "" {
		prompt += "\n\nUser: " + arguments
	}
	return ExpandedSkillInvocation{Name: skill.Name, Prompt: prompt}, nil
}
