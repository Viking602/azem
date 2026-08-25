package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/plugins"
)

type marketplaceActionPayload struct {
	Source string `json:"source,omitempty"`
	Name   string `json:"name,omitempty"`
	ID     string `json:"id,omitempty"`
	Scope  string `json:"scope,omitempty"`
	Force  bool   `json:"force,omitempty"`
}

func (s *Service) executeMarketplaceAction(ctx context.Context, action Action) error {
	if s.marketplace == nil {
		return errors.New("marketplace manager is unavailable")
	}
	payload, err := decodeMarketplaceAction(action.Payload)
	if err != nil {
		return err
	}
	target := firstMarketplaceValue(payload.ID, payload.Name, action.Target)
	scope := plugins.MarketplaceScope(firstMarketplaceValue(payload.Scope, action.Decision))
	filterMarketplace := ""
	state := string(action.Kind)
	reload := false
	switch action.Kind {
	case ActionMarketplaceAdd:
		source := firstMarketplaceValue(payload.Source, action.Target)
		_, err = s.marketplace.Add(ctx, source)
	case ActionMarketplaceRemove:
		err = s.marketplace.Remove(target)
	case ActionMarketplaceUpdate:
		_, err = s.marketplace.Update(ctx, target)
	case ActionMarketplaceList, ActionMarketplaceInstalled:
		// Read-only projection below.
	case ActionMarketplaceDiscover:
		filterMarketplace = target
	case ActionMarketplaceInstall:
		if scope == "" {
			scope = plugins.MarketplaceScopeUser
		}
		_, err = s.marketplace.Install(ctx, target, scope, payload.Force)
		reload = err == nil
	case ActionMarketplaceUninstall:
		if scope == "" {
			scope = plugins.MarketplaceScopeUser
		}
		err = s.marketplace.Uninstall(target, scope)
		reload = err == nil
	case ActionMarketplaceUpgrade:
		_, err = s.marketplace.Upgrade(ctx, target, scope)
		reload = err == nil
	case ActionMarketplaceEnable, ActionMarketplaceDisable:
		if scope == "" {
			scope = plugins.MarketplaceScopeUser
		}
		err = s.marketplace.SetEnabled(target, scope, action.Kind == ActionMarketplaceEnable)
		reload = err == nil
	default:
		return fmt.Errorf("unsupported marketplace action %q", action.Kind)
	}
	if err != nil {
		return err
	}
	if reload {
		if err := s.reloadPluginRuntime(ctx); err != nil {
			return err
		}
	}
	return s.emitMarketplaceCatalog(ctx, state, filterMarketplace)
}

func (s *Service) runMarketplaceAutoUpdate(ctx context.Context) {
	if s.marketplace == nil || s.cfg.Plugins.MarketplaceAutoUpdate == "off" {
		return
	}
	records, err := s.marketplace.List()
	if err != nil {
		s.emit(ctx, Event{Kind: EventMarketplaceCatalog, State: "error", Text: err.Error()})
		return
	}
	for _, record := range records {
		if time.Since(record.UpdatedAt) < 24*time.Hour {
			continue
		}
		if _, updateErr := s.marketplace.Update(ctx, record.Name); updateErr != nil {
			s.emit(ctx, Event{Kind: EventMarketplaceCatalog, State: "update_error", Text: updateErr.Error(), Data: map[string]string{"marketplace": record.Name}})
		}
	}
	if s.cfg.Plugins.MarketplaceAutoUpdate == "auto" {
		if upgraded, upgradeErr := s.marketplace.Upgrade(ctx, "", ""); upgradeErr != nil {
			s.emit(ctx, Event{Kind: EventMarketplaceCatalog, State: "upgrade_error", Text: upgradeErr.Error()})
		} else if len(upgraded) > 0 {
			_ = s.reloadPluginRuntime(ctx)
		}
	}
	_ = s.emitMarketplaceCatalog(ctx, "auto_update", "")
}

func (s *Service) MarketplaceCatalogSnapshot() (*MarketplaceCatalogPayload, error) {
	return s.marketplaceCatalogSnapshot("")
}

func (s *Service) marketplaceCatalogSnapshot(marketplace string) (*MarketplaceCatalogPayload, error) {
	if s.marketplace == nil {
		return nil, errors.New("marketplace manager is unavailable")
	}
	records, err := s.marketplace.List()
	if err != nil {
		return nil, err
	}
	for index := range records {
		records[index].CachePath = ""
	}
	available, err := s.marketplace.Discover(marketplace)
	if err != nil {
		return nil, err
	}
	installed, err := s.marketplace.Installed()
	if err != nil {
		return nil, err
	}
	upgrades, err := s.marketplace.AvailableUpgrades()
	if err != nil {
		return nil, err
	}
	for index := range installed {
		installed[index].Path = ""
	}
	for index := range upgrades {
		upgrades[index].Plugin.Path = ""
	}
	return &MarketplaceCatalogPayload{
		Marketplaces: records,
		Available:    available,
		Installed:    installed,
		Upgrades:     upgrades,
	}, nil
}

func (s *Service) emitMarketplaceCatalog(ctx context.Context, state, marketplace string) error {
	catalog, err := s.marketplaceCatalogSnapshot(marketplace)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventMarketplaceCatalog, State: state, MarketplaceCatalog: catalog})
	return nil
}

func decodeMarketplaceAction(payload json.RawMessage) (marketplaceActionPayload, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return marketplaceActionPayload{}, nil
	}
	var value marketplaceActionPayload
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode marketplace action: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return value, errors.New("decode marketplace action: trailing data")
	}
	return value, nil
}

func firstMarketplaceValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
