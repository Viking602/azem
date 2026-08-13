package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/hooks"
)

func (s *Service) setSkillEnabled(ctx context.Context, name string, enabled bool, sessionID string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 256 {
		return fmt.Errorf("skill name is required and must not exceed 256 characters")
	}
	if s.skillCatalog == nil {
		return fmt.Errorf("skills are unavailable")
	}
	s.routeMu.Lock()
	defer s.routeMu.Unlock()

	found := false
	for _, entry := range s.skillCatalog.Snapshot().Entries {
		if entry.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("skill %q is not available", name)
	}

	s.mu.Lock()
	currentSession := firstNonempty(sessionID, s.currentSession)
	skillsConfig := cloneSkillsConfig(s.cfg.Skills)
	s.mu.Unlock()
	skillsConfig.Disabled = setSkillMembership(skillsConfig.Disabled, name, !enabled)
	if !enabled {
		// Disabled skills cannot remain eager because eager skills resolve at
		// startup. Re-enabling intentionally returns the skill to on-demand mode.
		skillsConfig.Eager = setSkillMembership(skillsConfig.Eager, name, false)
	}

	persist := func() error {
		if err := s.dispatchLifecycle(ctx, hooks.ConfigChange, s.hookMetadata(currentSession, ""), func(e *hooks.Envelope) {
			e.Source, e.FilePath = "user_settings", s.configPath
		}); err != nil {
			return err
		}
		if s.configPath == "" {
			return nil
		}
		return s.ensureHookWatcher().writeConfig(s.configPath, func() error {
			return config.UpdateSkillsSelection(s.configPath, skillsConfig.Eager, skillsConfig.Disabled)
		})
	}
	if err := s.skillCatalog.UpdateConfig(skillsConfig, persist); err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg.Skills = cloneSkillsConfig(skillsConfig)
	s.mu.Unlock()
	if err := s.emitSkillCatalog(ctx, "availability_updated"); err != nil {
		return err
	}
	_ = s.emitContextProfile(ctx, currentSession)
	return nil
}

func setSkillMembership(names []string, name string, included bool) []string {
	result := make([]string, 0, len(names)+1)
	for _, existing := range names {
		if existing != name {
			result = append(result, existing)
		}
	}
	if included {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func cloneSkillsConfig(cfg config.SkillsConfig) config.SkillsConfig {
	cfg.AdditionalDirs = append([]string(nil), cfg.AdditionalDirs...)
	cfg.Eager = append([]string(nil), cfg.Eager...)
	cfg.Disabled = append([]string(nil), cfg.Disabled...)
	return cfg
}
