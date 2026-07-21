package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Usage is the durable last-known context and cache snapshot for a session.
// It mirrors the TUI footer counters so restart/resume can restore them.
type Usage struct {
	InputTokens         int    `json:"inputTokens,omitempty"`
	OutputTokens        int    `json:"outputTokens,omitempty"`
	CacheInputTokens    int    `json:"cacheInputTokens,omitempty"`
	CachedInputTokens   int    `json:"cachedInputTokens,omitempty"`
	MainCacheInput      int    `json:"mainCacheInput,omitempty"`
	MainCachedInput     int    `json:"mainCachedInput,omitempty"`
	ReasoningTokens     int    `json:"reasoningTokens,omitempty"`
	UncachedInputTokens int    `json:"uncachedInputTokens,omitempty"`
	CompactionInput     int    `json:"compactionInput,omitempty"`
	CompactionCached    int    `json:"compactionCached,omitempty"`
	CompactionOutput    int    `json:"compactionOutput,omitempty"`
	CompactionReasoning int    `json:"compactionReasoning,omitempty"`
	CompactionUncached  int    `json:"compactionUncached,omitempty"`
	ContextLimit        int    `json:"contextLimit,omitempty"`
	CacheReported       bool   `json:"cacheReported,omitempty"`
	MainCacheReported   bool   `json:"mainCacheReported,omitempty"`
	LastRequestKind     string `json:"lastRequestKind,omitempty"`
	LastProvider        string `json:"lastProvider,omitempty"`
	LastModel           string `json:"lastModel,omitempty"`
	LastTransport       string `json:"lastTransport,omitempty"`
}

func (u Usage) IsZero() bool {
	return u == (Usage{})
}

func (u Usage) Clone() Usage { return u }

// Reset clears per-turn occupancy and cache counters while preserving the
// known context window limit from the model catalog.
func (u *Usage) Reset() {
	if u == nil {
		return
	}
	limit := u.ContextLimit
	*u = Usage{ContextLimit: limit}
}

// Apply updates the snapshot from a context-usage event payload. The rules match
// the TUI footer: occupancy fields are replaced, cache counters accumulate.
func (u *Usage) Apply(data map[string]string) {
	if u == nil || data == nil {
		return
	}
	requestKind := data["requestKind"]
	if value, ok := atoiData(data, "inputTokens"); ok {
		if data["aggregateOnly"] != "true" {
			u.InputTokens = value
			if data["cacheStatus"] == "reported" {
				u.MainCacheInput += value
			}
		}
		if data["cacheStatus"] == "reported" {
			u.CacheInputTokens += value
		}
		if requestKind == "compaction" {
			u.CompactionInput += value
		}
	}
	if value, ok := atoiData(data, "cachedInputTokens"); ok {
		u.CachedInputTokens += value
		u.CacheReported = true
		if data["aggregateOnly"] != "true" {
			u.MainCachedInput += value
			u.MainCacheReported = true
		}
		if requestKind == "compaction" {
			u.CompactionCached += value
		}
	}
	if value, ok := atoiData(data, "outputTokens"); ok {
		if data["aggregateOnly"] != "true" {
			u.OutputTokens = value
		}
		if requestKind == "compaction" {
			u.CompactionOutput += value
		}
	}
	if value, ok := atoiData(data, "reasoningTokens"); ok {
		if requestKind == "compaction" {
			u.CompactionReasoning += value
		} else if requestKind == "main" {
			u.ReasoningTokens = value
		}
	}
	if value, ok := atoiData(data, "uncachedInputTokens"); ok {
		if requestKind == "compaction" {
			u.CompactionUncached += value
		} else if requestKind == "main" {
			u.UncachedInputTokens = value
		}
	}
	if value, ok := atoiData(data, "contextLimit"); ok {
		u.ContextLimit = value
	}
	if requestKind != "" {
		u.LastRequestKind = requestKind
	}
	for key, destination := range map[string]*string{"provider": &u.LastProvider, "model": &u.LastModel, "transport": &u.LastTransport} {
		if value := strings.TrimSpace(data[key]); value != "" {
			*destination = value
		}
	}
}

func (u Usage) Data() map[string]string {
	if u.IsZero() {
		return nil
	}
	data := map[string]string{
		"inputTokens":         strconv.Itoa(u.InputTokens),
		"outputTokens":        strconv.Itoa(u.OutputTokens),
		"cacheInputTokens":    strconv.Itoa(u.CacheInputTokens),
		"cachedInputTokens":   strconv.Itoa(u.CachedInputTokens),
		"mainCacheInput":      strconv.Itoa(u.MainCacheInput),
		"mainCachedInput":     strconv.Itoa(u.MainCachedInput),
		"reasoningTokens":     strconv.Itoa(u.ReasoningTokens),
		"uncachedInputTokens": strconv.Itoa(u.UncachedInputTokens),
		"compactionInput":     strconv.Itoa(u.CompactionInput),
		"compactionCached":    strconv.Itoa(u.CompactionCached),
		"compactionOutput":    strconv.Itoa(u.CompactionOutput),
		"compactionReasoning": strconv.Itoa(u.CompactionReasoning),
		"compactionUncached":  strconv.Itoa(u.CompactionUncached),
		"contextLimit":        strconv.Itoa(u.ContextLimit),
	}
	for key, value := range map[string]string{"lastRequestKind": u.LastRequestKind, "lastProvider": u.LastProvider, "lastModel": u.LastModel, "lastTransport": u.LastTransport} {
		if value != "" {
			data[key] = value
		}
	}
	if u.CacheReported {
		data["cacheReported"] = "true"
	}
	if u.MainCacheReported {
		data["mainCacheReported"] = "true"
	}
	return data
}

func DecodeUsage(raw []byte) (Usage, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return Usage{}, nil
	}
	var value Usage
	if err := json.Unmarshal(raw, &value); err != nil {
		return Usage{}, fmt.Errorf("decode session usage: %w", err)
	}
	return value, nil
}

func (s *Service) UpdateUsage(ctx context.Context, sessionID string, usage Usage) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	encoded, err := json.Marshal(usage)
	if err != nil {
		return fmt.Errorf("encode session usage: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE session_projections SET usage=? WHERE session_id=?`, encoded, sessionID)
	if err != nil {
		return fmt.Errorf("update session usage: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("session %q not found", sessionID)
	}
	return nil
}

func atoiData(data map[string]string, key string) (int, bool) {
	raw := data[key]
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return value, true
}
