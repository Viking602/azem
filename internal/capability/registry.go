package capability

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
)

type Kind string

const (
	KindProvider  Kind = "provider"
	KindModel     Kind = "model"
	KindTool      Kind = "tool"
	KindResource  Kind = "resource"
	KindMode      Kind = "mode"
	KindExtension Kind = "extension"
	KindProtocol  Kind = "protocol"
)

type Descriptor struct {
	ID          string            `json:"id"`
	Kind        Kind              `json:"kind"`
	Source      string            `json:"source"`
	Description string            `json:"description,omitempty"`
	Enabled     bool              `json:"enabled"`
	Operations  []string          `json:"operations,omitempty"`
	Modalities  []string          `json:"modalities,omitempty"`
	InputSchema json.RawMessage   `json:"inputSchema,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Source func() ([]Descriptor, error)

var (
	ErrDuplicateSource     = errors.New("capability source is already registered")
	ErrDuplicateCapability = errors.New("capability is registered by more than one source")
)

type Registry struct {
	mu      sync.RWMutex
	sources map[string]Source
}

func NewRegistry() *Registry {
	return &Registry{sources: make(map[string]Source)}
}

func (registry *Registry) RegisterSource(name string, source Source) error {
	name = strings.TrimSpace(name)
	if registry == nil || name == "" || source == nil {
		return errors.New("capability source name and resolver are required")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.sources[name]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateSource, name)
	}
	registry.sources[name] = source
	return nil
}

func (registry *Registry) Snapshot() ([]Descriptor, error) {
	if registry == nil {
		return nil, nil
	}
	registry.mu.RLock()
	names := make([]string, 0, len(registry.sources))
	sources := make(map[string]Source, len(registry.sources))
	for name, source := range registry.sources {
		names = append(names, name)
		sources[name] = source
	}
	registry.mu.RUnlock()
	sort.Strings(names)

	result := make([]Descriptor, 0)
	seen := make(map[string]string)
	for _, name := range names {
		entries, err := sources[name]()
		if err != nil {
			return nil, fmt.Errorf("load capability source %s: %w", name, err)
		}
		for _, entry := range entries {
			entry.ID = strings.TrimSpace(entry.ID)
			if entry.ID == "" || entry.Kind == "" {
				return nil, fmt.Errorf("capability source %s returned an incomplete descriptor", name)
			}
			if entry.Source == "" {
				entry.Source = name
			}
			key := string(entry.Kind) + "\x00" + entry.ID
			if previous, duplicate := seen[key]; duplicate {
				return nil, fmt.Errorf("%w: %s/%s from %s and %s", ErrDuplicateCapability, entry.Kind, entry.ID, previous, name)
			}
			seen[key] = name
			result = append(result, cloneDescriptor(entry))
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Kind == result[right].Kind {
			return result[left].ID < result[right].ID
		}
		return result[left].Kind < result[right].Kind
	})
	return result, nil
}

func (registry *Registry) Resolve(kind Kind, id string) (Descriptor, bool, error) {
	snapshot, err := registry.Snapshot()
	if err != nil {
		return Descriptor{}, false, err
	}
	for _, descriptor := range snapshot {
		if descriptor.Kind == kind && descriptor.ID == id {
			return descriptor, true, nil
		}
	}
	return Descriptor{}, false, nil
}

func Static(entries ...Descriptor) Source {
	cloned := make([]Descriptor, len(entries))
	for index := range entries {
		cloned[index] = cloneDescriptor(entries[index])
	}
	return func() ([]Descriptor, error) {
		result := make([]Descriptor, len(cloned))
		for index := range cloned {
			result[index] = cloneDescriptor(cloned[index])
		}
		return result, nil
	}
}

func cloneDescriptor(descriptor Descriptor) Descriptor {
	descriptor.Operations = slices.Clone(descriptor.Operations)
	descriptor.Modalities = slices.Clone(descriptor.Modalities)
	descriptor.InputSchema = append(json.RawMessage(nil), descriptor.InputSchema...)
	descriptor.Metadata = maps.Clone(descriptor.Metadata)
	return descriptor
}
