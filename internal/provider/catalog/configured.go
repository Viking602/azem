package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
)

func (s *Service) ConfiguredModels(ctx context.Context, providerID string) ([]config.LLMuxModelConfig, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("model catalog is unavailable")
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil, fmt.Errorf("provider is required")
	}
	rows, err := dbgen.New(s.db).ListLLMuxProviderModels(ctx, providerID)
	if err != nil {
		return nil, err
	}
	models := make([]config.LLMuxModelConfig, 0, len(rows))
	for _, row := range rows {
		var model config.LLMuxModelConfig
		if err := json.Unmarshal(row.Payload, &model); err != nil {
			return nil, fmt.Errorf("decode %s model %s: %w", providerID, row.ModelID, err)
		}
		if strings.TrimSpace(model.ID) == "" {
			model.ID = row.ModelID
		}
		models = append(models, model)
	}
	return models, nil
}

func (s *Service) ReplaceConfiguredModels(ctx context.Context, providerID string, models []config.LLMuxModelConfig) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("model catalog is unavailable")
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return fmt.Errorf("provider is required")
	}
	if len(models) > 2048 {
		return fmt.Errorf("providers.llmux.%s.models must contain at most 2048 models", providerID)
	}
	now := time.Now().UTC().UnixNano()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := dbgen.New(s.db).WithTx(tx)
	if err := queries.DeleteLLMuxProviderModels(ctx, providerID); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			return fmt.Errorf("model id is required")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate model %q", id)
		}
		seen[id] = struct{}{}
		model.ID = id
		payload, err := json.Marshal(model)
		if err != nil {
			return err
		}
		if err := queries.InsertLLMuxProviderModel(ctx, dbgen.InsertLLMuxProviderModelParams{
			ProviderID: providerID, ModelID: id, Payload: payload, UpdatedAt: now,
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) SetConfiguredModelDisabled(ctx context.Context, providerID, modelID string, disabled bool) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("model catalog is unavailable")
	}
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return fmt.Errorf("provider and model are required")
	}
	row, err := dbgen.New(s.db).GetLLMuxProviderModel(ctx, dbgen.GetLLMuxProviderModelParams{ProviderID: providerID, ModelID: modelID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("model %q is not configured for %s", modelID, providerID)
		}
		return err
	}
	var model config.LLMuxModelConfig
	if err := json.Unmarshal(row.Payload, &model); err != nil {
		return fmt.Errorf("decode %s model %s: %w", providerID, modelID, err)
	}
	model.ID = modelID
	model.Disabled = disabled
	payload, err := json.Marshal(model)
	if err != nil {
		return err
	}
	return dbgen.New(s.db).UpdateLLMuxProviderModel(ctx, dbgen.UpdateLLMuxProviderModelParams{
		Payload: payload, UpdatedAt: time.Now().UTC().UnixNano(), ProviderID: providerID, ModelID: modelID,
	})
}
