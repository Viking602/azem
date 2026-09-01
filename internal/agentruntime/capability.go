package agentruntime

import "strings"

const (
	// HydaelynSelfNamespace is reserved for framework-owned capabilities.
	HydaelynSelfNamespace = "hydaelyn.self."

	CapabilityNameSelfProfile          = HydaelynSelfNamespace + "profile"
	CapabilityNameSelfMemoryRead       = HydaelynSelfNamespace + "memory.read"
	CapabilityNameSelfHistory          = HydaelynSelfNamespace + "history"
	CapabilityNameSelfSummarizeHistory = HydaelynSelfNamespace + "summarize_history"
)

// ValidateCapabilityName rejects empty names and user registrations under
// HydaelynSelfNamespace. Implementations are reserved; SaveCapability must
// not accept these names until a later release ships them.
func ValidateCapabilityName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ErrInvalidCommand
	}
	if strings.HasPrefix(trimmed, HydaelynSelfNamespace) {
		return ErrCapabilityNameReserved
	}
	return nil
}
