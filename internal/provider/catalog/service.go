package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
)

const (
	DefaultChatGPTCatalogURL     = "https://chatgpt.com/backend-api/codex/models"
	DefaultGrokCatalogURL        = "https://api.x.ai/v1/models"
	DefaultGrokLanguageModelsURL = "https://api.x.ai/v1/language-models"
	DefaultChatGPTClientVersion  = "0.144.3"
)

type Model struct {
	ID                string         `json:"id"`
	Name              string         `json:"name,omitempty"`
	Description       string         `json:"description,omitempty"`
	ContextWindow     int            `json:"contextWindow,omitempty"`
	ReasoningLevels   []string       `json:"reasoningLevels,omitempty"`
	DefaultReasoning  string         `json:"defaultReasoning,omitempty"`
	SupportsTools     bool           `json:"supportsTools"`
	SupportsParallel  bool           `json:"supportsParallel"`
	SupportsReasoning bool           `json:"supportsReasoning"`
	Aliases           []string       `json:"aliases,omitempty"`
	InputModalities   []string       `json:"inputModalities,omitempty"`
	OutputModalities  []string       `json:"outputModalities,omitempty"`
	Pricing           map[string]any `json:"pricing,omitempty"`
}

type Result struct {
	Provider  string
	AccountID string
	Models    []Model
	FetchedAt time.Time
	ExpiresAt time.Time
	Stale     bool
	Warning   string
}

type Service struct {
	db                  *sql.DB
	auth                *auth.Service
	TTL                 map[string]time.Duration
	Endpoints           map[string]string
	AdditionalEndpoints map[string][]string
}

func NewService(db *sql.DB, authentication *auth.Service) *Service {
	return &Service{
		db: db, auth: authentication,
		TTL:                 map[string]time.Duration{"chatgpt": 5 * time.Minute, "grok": 5 * time.Minute},
		Endpoints:           map[string]string{"chatgpt": DefaultChatGPTCatalogURL, "grok": DefaultGrokCatalogURL},
		AdditionalEndpoints: map[string][]string{"grok": {DefaultGrokLanguageModelsURL}},
	}
}

func (s *Service) List(ctx context.Context, provider string, accountID string, force bool) (Result, error) {
	cached, found, err := s.load(ctx, provider, accountID)
	if err != nil {
		return Result{}, err
	}
	if found && !force && time.Now().Before(cached.ExpiresAt) {
		return cached, nil
	}
	fresh, err := s.fetch(ctx, provider, accountID, cached)
	if err != nil {
		if found {
			cached.Stale = true
			cached.Warning = err.Error()
			return cached, nil
		}
		return Result{}, err
	}
	return fresh, nil
}

func (s *Service) ValidateSelection(ctx context.Context, provider string, accountID string, modelID string) error {
	result, err := s.List(ctx, provider, accountID, false)
	if err != nil {
		return err
	}
	for _, model := range result.Models {
		if model.ID == modelID {
			return nil
		}
	}
	return fmt.Errorf("model %q is not present in the %s catalog for account %s", modelID, provider, accountID)
}
func (s *Service) fetch(ctx context.Context, provider string, accountID string, cached Result) (Result, error) {
	primary := s.Endpoints[provider]
	if primary == "" {
		return Result{}, fmt.Errorf("unsupported provider %q", provider)
	}
	if provider == "chatgpt" {
		parsed, err := url.Parse(primary)
		if err != nil {
			return Result{}, fmt.Errorf("parse ChatGPT catalog URL: %w", err)
		}
		query := parsed.Query()
		query.Set("client_version", DefaultChatGPTClientVersion)
		parsed.RawQuery = query.Encode()
		primary = parsed.String()
	}
	endpoints := append([]string{primary}, s.AdditionalEndpoints[provider]...)
	models := make([]Model, 0, 16)
	etag := ""
	if provider == "chatgpt" && len(cached.Models) > 0 {
		etag = s.cachedETag(ctx, provider, accountID)
	}
	for sourceIndex, endpoint := range endpoints {
		next := endpoint
		for page := range 20 {
			currentURL := next
			response, err := s.auth.DoWithRefresh(ctx, provider, accountID, func(auth.Credential) (*http.Request, error) {
				request, err := http.NewRequest(http.MethodGet, currentURL, nil)
				if err == nil && provider == "chatgpt" {
					request.Header.Set("originator", "codex_cli_rs")
					request.Header.Set("User-Agent", "azem/1")
				}
				if err == nil && sourceIndex == 0 && page == 0 && etag != "" {
					request.Header.Set("If-None-Match", etag)
				}
				return request, err
			})
			if err != nil {
				return Result{}, err
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
			response.Body.Close()
			if readErr != nil {
				return Result{}, readErr
			}
			if response.StatusCode == http.StatusNotModified && sourceIndex == 0 && len(endpoints) == 1 && len(cached.Models) > 0 {
				return s.extend(ctx, cached, s.ttl(provider))
			}
			if response.StatusCode/100 != 2 {
				return Result{}, catalogHTTPError(provider, response.StatusCode, body)
			}
			if sourceIndex == 0 && page == 0 {
				etag = response.Header.Get("ETag")
			}
			pageModels, hasMore, after, err := decode(provider, body)
			if err != nil {
				return Result{}, err
			}
			models = append(models, pageModels...)
			if !hasMore {
				break
			}
			if page == 19 {
				return Result{}, fmt.Errorf("%s catalog exceeded 20 pages", provider)
			}
			if after == "" {
				return Result{}, fmt.Errorf("%s catalog pagination omitted cursor", provider)
			}
			parsed, err := url.Parse(endpoint)
			if err != nil {
				return Result{}, err
			}
			query := parsed.Query()
			query.Set("after", after)
			parsed.RawQuery = query.Encode()
			next = parsed.String()
		}
	}
	if len(models) == 0 {
		return Result{}, fmt.Errorf("%s catalog returned no models", provider)
	}
	sort.SliceStable(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	models = mergeDuplicates(models)
	now := time.Now().UTC()
	result := Result{Provider: provider, AccountID: accountID, Models: models, FetchedAt: now, ExpiresAt: now.Add(s.ttl(provider))}
	if err := s.save(ctx, result, etag); err != nil {
		return Result{}, err
	}
	return result, nil
}

func catalogHTTPError(provider string, status int, body []byte) error {
	message := strings.Join(strings.Fields(string(body)), " ")
	if len(message) > 512 {
		message = message[:512] + "..."
	}
	if message == "" {
		return fmt.Errorf("%s catalog returned HTTP %d", provider, status)
	}
	return fmt.Errorf("%s catalog returned HTTP %d: %s", provider, status, message)
}

type reasoningLevels []string

func (levels *reasoningLevels) UnmarshalJSON(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		var name string
		if json.Unmarshal(value, &name) == nil && name != "" {
			result = append(result, name)
			continue
		}
		var preset struct {
			Effort string `json:"effort"`
		}
		if err := json.Unmarshal(value, &preset); err != nil {
			return err
		}
		if preset.Effort != "" {
			result = append(result, preset.Effort)
		}
	}
	*levels = result
	return nil
}

type grokCatalogModel struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Aliases          []string       `json:"aliases"`
	ContextWindow    int            `json:"context_window"`
	ContextLength    int            `json:"context_length"`
	Capabilities     []string       `json:"capabilities"`
	InputModalities  []string       `json:"input_modalities"`
	OutputModalities []string       `json:"output_modalities"`
	Pricing          map[string]any `json:"pricing"`
}

func decode(provider string, data []byte) ([]Model, bool, string, error) {
	switch provider {
	case "chatgpt":
		var payload struct {
			Models []struct {
				ID               string          `json:"id"`
				Slug             string          `json:"slug"`
				Name             string          `json:"name"`
				Title            string          `json:"title"`
				DisplayName      string          `json:"display_name"`
				Description      string          `json:"description"`
				ContextWindow    int             `json:"context_window"`
				DefaultReasoning string          `json:"default_reasoning_level"`
				ReasoningLevels  reasoningLevels `json:"supported_reasoning_levels"`
				SupportsTools    *bool           `json:"supports_tools"`
				SupportsParallel *bool           `json:"supports_parallel_tool_calls"`
				InputModalities  []string        `json:"input_modalities"`
			} `json:"models"`
			Data    json.RawMessage `json:"data"`
			HasMore bool            `json:"has_more"`
			After   string          `json:"after"`
			LastID  string          `json:"last_id"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, false, "", err
		}
		if len(payload.Models) == 0 && len(payload.Data) > 0 {
			_ = json.Unmarshal(payload.Data, &payload.Models)
		}
		models := make([]Model, 0, len(payload.Models))
		for _, item := range payload.Models {
			id := first(item.ID, item.Slug)
			if id == "" {
				continue
			}
			model := Model{
				ID: id, Name: first(item.Name, item.DisplayName, item.Title, id),
				Description: item.Description, ContextWindow: item.ContextWindow,
				ReasoningLevels: []string(item.ReasoningLevels), DefaultReasoning: item.DefaultReasoning,
				SupportsReasoning: len(item.ReasoningLevels) > 0 || item.DefaultReasoning != "",
				InputModalities:   item.InputModalities,
			}
			model.SupportsTools = item.SupportsTools == nil || *item.SupportsTools
			model.SupportsParallel = item.SupportsParallel != nil && *item.SupportsParallel
			models = append(models, model)
		}
		return models, payload.HasMore, first(payload.After, payload.LastID), nil
	case "grok":
		var payload struct {
			Data    []grokCatalogModel `json:"data"`
			Models  []grokCatalogModel `json:"models"`
			HasMore bool               `json:"has_more"`
			After   string             `json:"after"`
			LastID  string             `json:"last_id"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, false, "", err
		}
		items := payload.Data
		if len(items) == 0 {
			items = payload.Models
		}
		models := make([]Model, 0, len(items))
		for _, item := range items {
			if item.ID == "" {
				continue
			}
			contextWindow := item.ContextLength
			if contextWindow == 0 {
				contextWindow = item.ContextWindow
			}
			model := Model{ID: item.ID, Name: first(item.Name, item.ID), Aliases: item.Aliases, ContextWindow: contextWindow, InputModalities: item.InputModalities, OutputModalities: item.OutputModalities, Pricing: item.Pricing}
			for _, capability := range item.Capabilities {
				switch capability {
				case "tools", "tool_use":
					model.SupportsTools = true
				case "parallel_tool_calls":
					model.SupportsParallel = true
				case "reasoning":
					model.SupportsReasoning = true
				}
			}
			models = append(models, model)
		}
		return models, payload.HasMore, first(payload.After, payload.LastID), nil
	default:
		return nil, false, "", fmt.Errorf("unsupported provider %q", provider)
	}
}

func (s *Service) load(ctx context.Context, provider string, accountID string) (Result, bool, error) {
	rows, err := dbgen.New(s.db).ListCatalog(ctx, dbgen.ListCatalogParams{ProviderID: provider, AccountID: accountID})
	if err != nil {
		return Result{}, false, err
	}
	result := Result{Provider: provider, AccountID: accountID}
	for _, row := range rows {
		var model Model
		if err := json.Unmarshal(row.Data, &model); err != nil {
			return Result{}, false, err
		}
		result.Models = append(result.Models, model)
		result.FetchedAt, result.ExpiresAt = time.Unix(0, row.FetchedAt).UTC(), time.Unix(0, row.ExpiresAt).UTC()
	}
	return result, len(result.Models) > 0, nil
}

func (s *Service) save(ctx context.Context, result Result, etag string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := dbgen.New(s.db).WithTx(tx)
	if err := queries.DeleteCatalog(ctx, dbgen.DeleteCatalogParams{ProviderID: result.Provider, AccountID: result.AccountID}); err != nil {
		return err
	}
	for _, model := range result.Models {
		data, err := json.Marshal(model)
		if err != nil {
			return err
		}
		if err := queries.InsertCatalogModel(ctx, dbgen.InsertCatalogModelParams{ProviderID: result.Provider, AccountID: result.AccountID, ModelID: model.ID, Etag: etag, FetchedAt: result.FetchedAt.UnixNano(), ExpiresAt: result.ExpiresAt.UnixNano(), Data: data}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) extend(ctx context.Context, cached Result, ttl time.Duration) (Result, error) {
	cached.ExpiresAt = time.Now().UTC().Add(ttl)
	err := dbgen.New(s.db).ExtendCatalog(ctx, dbgen.ExtendCatalogParams{ExpiresAt: cached.ExpiresAt.UnixNano(), ProviderID: cached.Provider, AccountID: cached.AccountID})
	return cached, err
}

func (s *Service) cachedETag(ctx context.Context, provider string, accountID string) string {
	etag, _ := dbgen.New(s.db).GetCatalogETag(ctx, dbgen.GetCatalogETagParams{ProviderID: provider, AccountID: accountID})
	return etag
}

func (s *Service) ttl(provider string) time.Duration {
	if ttl := s.TTL[provider]; ttl > 0 {
		return ttl
	}
	return 5 * time.Minute
}

func mergeDuplicates(models []Model) []Model {
	if len(models) < 2 {
		return models
	}
	output := models[:1]
	for _, model := range models[1:] {
		current := &output[len(output)-1]
		if model.ID != current.ID {
			output = append(output, model)
			continue
		}
		current.Name = first(current.Name, model.Name)
		current.Description = first(current.Description, model.Description)
		if current.ContextWindow == 0 {
			current.ContextWindow = model.ContextWindow
		}
		current.Aliases = appendUnique(current.Aliases, model.Aliases...)
		current.InputModalities = appendUnique(current.InputModalities, model.InputModalities...)
		current.OutputModalities = appendUnique(current.OutputModalities, model.OutputModalities...)
		current.ReasoningLevels = appendUnique(current.ReasoningLevels, model.ReasoningLevels...)
		current.SupportsTools = current.SupportsTools || model.SupportsTools
		current.SupportsParallel = current.SupportsParallel || model.SupportsParallel
		current.SupportsReasoning = current.SupportsReasoning || model.SupportsReasoning
		if current.Pricing == nil {
			current.Pricing = model.Pricing
		}
	}
	return output
}

func appendUnique(values []string, additions ...string) []string {
	for _, addition := range additions {
		found := false
		for _, value := range values {
			if value == addition {
				found = true
				break
			}
		}
		if !found {
			values = append(values, addition)
		}
	}
	return values
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
