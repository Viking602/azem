package agentruntime

import "time"

// Budget bounds aggregate work for one durable execution. Zero or negative
// values leave that dimension unbounded.
type Budget struct {
	MaxCredits     int64         `json:"maxCredits,omitempty"`
	MaxTokens      int64         `json:"maxTokens,omitempty"`
	MaxToolCalls   int           `json:"maxToolCalls,omitempty"`
	MaxRuntime     time.Duration `json:"maxRuntime,omitempty"`
	MaxModelCalls  int           `json:"maxModelCalls,omitempty"`
	MaxActionCalls int           `json:"maxActionCalls,omitempty"`
}

// Quota bounds aggregate spend across runs in one time window.
type Quota struct {
	Window           time.Duration `json:"window"`
	MaxRunsPerWindow int           `json:"maxRunsPerWindow,omitempty"`
	MaxCredits       int64         `json:"maxCredits,omitempty"`
}

// GovernancePolicy is Azem's declarative admission and effect policy. It is
// application metadata, not a provider-visible tool or Agent SDK definition.
type GovernancePolicy struct {
	Budget Budget `json:"budget,omitempty"`
	Quota  Quota  `json:"quota,omitempty"`

	AllowedCapabilities []string `json:"allowedCapabilities,omitempty"`
	DeniedCapabilities  []string `json:"deniedCapabilities,omitempty"`

	ApprovalRequiredFor []ToolEffectType `json:"approvalRequiredFor,omitempty"`

	MaxConcurrentRuns     int `json:"maxConcurrentRuns,omitempty"`
	PauseOnExcessFailures int `json:"pauseOnExcessFailures,omitempty"`
}
