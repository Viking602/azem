package llmuxdriver

import (
	"sort"
	"strings"

	llmuxcatalog "github.com/Viking602/llmux/provider/catalog"
	"github.com/Viking602/llmux/provider/openai/compat"
)

type Profile struct {
	ID            string
	DisplayName   string
	Backend       string
	BaseURL       string
	EnvKey        string
	AllowEmptyKey bool
	APIKeyHeader  string
	APIKeyPrefix  string
}

var nativeProfiles = []Profile{
	{ID: "anthropic", DisplayName: "Anthropic", Backend: "anthropic", BaseURL: "https://api.anthropic.com", EnvKey: "ANTHROPIC_API_KEY"},
	{ID: "cohere", DisplayName: "Cohere", Backend: "cohere", BaseURL: "https://api.cohere.com/v2", EnvKey: "COHERE_API_KEY"},
	{ID: "google", DisplayName: "Google Gemini", Backend: "google", BaseURL: "https://generativelanguage.googleapis.com/v1beta", EnvKey: "GOOGLE_GENERATIVE_AI_API_KEY"},
	{ID: "mistral", DisplayName: "Mistral", Backend: "mistral", BaseURL: "https://api.mistral.ai/v1", EnvKey: "MISTRAL_API_KEY"},
	{ID: "openai", DisplayName: "OpenAI API", Backend: "openai", BaseURL: "https://api.openai.com/v1", EnvKey: "OPENAI_API_KEY"},
	{ID: "xai", DisplayName: "xAI API", Backend: "xai", BaseURL: "https://api.x.ai/v1", EnvKey: "XAI_API_KEY"},
}

func Profiles() []Profile {
	profiles := append([]Profile(nil), nativeProfiles...)
	seen := map[string]bool{"chatgpt": true, "grok": true}
	for _, profile := range nativeProfiles {
		seen[profile.ID] = true
	}
	for _, current := range compat.All() {
		if seen[current.ID] {
			continue
		}
		seen[current.ID] = true
		backend := string(llmuxcatalog.BackendOpenAICompat)
		if provider, ok := llmuxcatalog.Lookup(current.ID); ok {
			backend = string(provider.Backend)
		}
		baseURL := current.BaseURL
		if override := map[string]string{
			"freemodel": "https://cc.freemodel.dev/v1",
			"opencode":  "https://opencode.ai/zen/v1",
			"xpersona":  "https://www.xpersona.co/v1",
		}[current.ID]; baseURL == "" {
			baseURL = override
		}
		profiles = append(profiles, Profile{
			ID: current.ID, DisplayName: current.DisplayName, Backend: backend,
			BaseURL: baseURL, EnvKey: current.EnvKey, AllowEmptyKey: current.AllowEmptyAPIKey,
			APIKeyHeader: current.APIKeyHeader, APIKeyPrefix: current.APIKeyPrefix,
		})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	return profiles
}

func LookupProfile(id string) (Profile, bool) {
	id = CanonicalProviderID(id)
	for _, profile := range Profiles() {
		if profile.ID == id {
			return profile, true
		}
	}
	return Profile{}, false
}

func CanonicalProviderID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if profile, ok := compat.Lookup(id); ok {
		return profile.ID
	}
	return strings.ReplaceAll(id, "_", "-")
}
