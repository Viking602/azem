package tui

import (
	"os"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/i18n"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/recap"
	"github.com/Viking602/azem/internal/session"
)

type BlockKind string

const (
	BlockUser      BlockKind = "user"
	BlockThinking  BlockKind = "thinking"
	BlockAssistant BlockKind = "assistant"
	BlockTool      BlockKind = "tool"
	BlockApproval  BlockKind = "approval"
	BlockAgent     BlockKind = "agent"
	BlockDiff      BlockKind = "diff"
	BlockHook      BlockKind = "hook"
	BlockError     BlockKind = "error"
)

type HookRunView struct {
	Key        string
	Event      string
	Name       string
	Source     string
	State      string
	DurationMS int64
	Reason     string
	Output     string
	Truncated  bool
}

type Block struct {
	ID         string
	Kind       BlockKind
	RunID      string
	ToolCallID string
	Title      string
	Arguments  string
	Content    string
	Collapsed  bool
	State      string
	Orphaned   bool
	Hooks      []HookRunView
}

type transcriptBlockLayout struct {
	block          Block
	selected       bool
	animationFrame int
	lines          []string
}

type transcriptLayoutCache struct {
	contentWidth int
	initialized  bool
	blocks       []transcriptBlockLayout
	lines        []string
}

type recapLayoutCache struct {
	contentWidth int
	language     string
	initialized  bool
	value        recap.Recap
	lines        []string
}

type transcriptSelection struct {
	startX int
	startY int
	endX   int
	endY   int
}

type Overlay string

const (
	OverlayNone        Overlay = ""
	OverlayHelp        Overlay = "help"
	OverlayCommand     Overlay = "command"
	OverlayProvider    Overlay = "provider"
	OverlayModel       Overlay = "model"
	OverlaySkills      Overlay = "skills"
	OverlayLanguage    Overlay = "language"
	OverlayReasoning   Overlay = "reasoning"
	OverlaySessions    Overlay = "sessions"
	OverlayApproval    Overlay = "approval"
	OverlayCancel      Overlay = "cancel"
	OverlayDiff        Overlay = "diff"
	OverlayAgents      Overlay = "agents"
	OverlayTodos       Overlay = "todos"
	OverlayMemory      Overlay = "memory"
	OverlayRecap       Overlay = "recap"
	OverlayAgentDetail Overlay = "agent_detail"
	OverlayAgentTypes  Overlay = "agent_types"
	OverlayPersonas    Overlay = "personas"
	OverlayMCP         Overlay = "mcp"
	OverlayRecovery    Overlay = "recovery"
	OverlayError       Overlay = "error"
)

type focusArea uint8

const (
	focusComposer focusArea = iota
	focusTranscript
	focusOverlay
)

type AgentView struct {
	ID                 string
	Role               string
	State              string
	Summary            string
	Description        string
	Model              string
	Background         bool
	CapabilityMode     string
	RequestedIsolation string
	Isolation          string
	CWD                string
	ParentRunID        string
	ParentToolCallID   string
	ChildRunID         string
	Activity           string
	Warning            string
	WorktreePath       string
	ToolCalls          int
	Turns              int
	TokensUsed         int
	ElapsedMS          int64
	Blocks             []Block
}

type AgentCatalogView struct {
	Name           string
	Description    string
	Persona        string
	Model          string
	Reasoning      string
	CapabilityMode string
	Isolation      string
	Source         string
	Enabled        bool
}

type SkillCatalogView = app.SkillCatalogEntry
type TodoView = session.TodoList

type MCPView struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	ToolCount int    `json:"toolCount"`
	Error     string `json:"error"`
}

type ModelChoice = catalog.Model

type modelPickerEntry struct {
	Provider string
	Model    ModelChoice
}

type SessionChoice struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
	UpdatedAt  string `json:"updatedAt"`
}

type AuthView struct {
	Provider    string `json:"provider"`
	AccountID   string `json:"accountId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Plan        string `json:"plan"`
	State       string `json:"state"`
}

type ApprovalView struct {
	ApprovalID string
	AgentID    string
	ToolCallID string
	Tool       string
	Target     string
	Risk       string
	Effect     string
	Action     string
	Diff       string
}

type RecoveryView struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	RunID    string `json:"runId"`
	TaskID   string `json:"taskId"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	State    string `json:"state"`
	TokenID  string `json:"tokenId"`
	ToolName string `json:"toolName"`
}

type UsageView struct {
	InputTokens       int
	CacheInputTokens  int
	CachedInputTokens int
	MainCacheInput    int
	MainCachedInput   int
	OutputTokens      int
	ContextLimit      int
	CacheReported     bool
	MainCacheReported bool
}

type AppModel struct {
	runtime             Runtime
	initialCmd          tea.Cmd
	theme               Theme
	catalog             i18n.Catalog
	composer            textarea.Model
	modelSearch         textinput.Model
	commandCursor       int
	width               int
	height              int
	sessionID           string
	runID               string
	lastRunID           string
	transcript          []Block
	transcriptLayout    *transcriptLayoutCache
	recapLayout         *recapLayoutCache
	transcriptTop       int
	transcriptCursor    int
	transcriptSelection *transcriptSelection
	focus               focusArea
	overlay             Overlay
	overlayCursor       int
	overlayScroll       int
	overlayPurpose      string
	provider            string
	model               string
	reasoning           string
	agentMode           string
	workspace           string
	status              string
	approvalMode        ApprovalMode
	autoReviewAvailable bool
	errorBanner         string
	quitting            bool
	reducedMotion       bool
	animationActive     bool
	animationFrame      int
	actionBusy          bool
	actionCancel        func()
	agents              []AgentView
	todo                TodoView
	todoHideCompleted   bool
	memories            []memory.Memory
	recap               *recap.Recap
	detailAgentID       string
	agentTypes          []AgentCatalogView
	personas            []AgentCatalogView
	skills              []SkillCatalogView
	skillDiagnostics    []app.SkillDiagnostic
	mcpServers          []MCPView
	models              []ModelChoice
	modelsByProvider    map[string][]ModelChoice
	sessions            []SessionChoice
	recovery            []RecoveryView
	auth                map[string]AuthView
	approval            *ApprovalView
	pendingApprovals    []ApprovalView
	usage               UsageView
}

func NewModel(runtime Runtime, workspace string, provider string, model string, reasoning string, mode string, initialSessionID ...string) AppModel {
	theme := DefaultTheme()
	catalog := i18n.Must(i18n.DefaultLanguage)
	composer := textarea.New()
	composer.Prompt = "› "
	composer.Placeholder = catalog.T("composer.placeholder")
	composer.ShowLineNumbers = false
	composer.CharLimit = 64 * 1024
	composer.DynamicHeight = true
	composer.MinHeight = 1
	composer.MaxHeight = 5
	composer.MaxContentHeight = 32
	composer.SetHeight(1)
	composer.SetWidth(76)
	composer.SetVirtualCursor(false)
	styles := composer.Styles()
	styles.Focused.Text = theme.Assistant
	styles.Focused.Prompt = theme.Header
	styles.Focused.Placeholder = theme.Muted
	styles.Blurred.Text = theme.Muted
	styles.Blurred.Prompt = theme.Muted
	styles.Blurred.Placeholder = theme.Muted
	styles.Cursor.Color = theme.Cursor.GetForeground()
	styles.Cursor.Shape = tea.CursorBar
	composer.SetStyles(styles)
	modelSearch := textinput.New()
	modelSearch.Prompt = catalog.T("search.prompt")
	modelSearch.Placeholder = catalog.T("search.placeholder")
	modelSearch.CharLimit = 128
	modelSearch.SetWidth(64)
	searchStyles := modelSearch.Styles()
	searchStyles.Focused.Text = theme.Assistant
	searchStyles.Focused.Prompt = theme.Header
	searchStyles.Focused.Placeholder = theme.Muted
	searchStyles.Blurred.Text = theme.Muted
	searchStyles.Blurred.Prompt = theme.Muted
	searchStyles.Blurred.Placeholder = theme.Muted
	searchStyles.Cursor.Color = theme.Cursor.GetForeground()
	searchStyles.Cursor.Shape = tea.CursorBar
	modelSearch.SetStyles(searchStyles)
	focus := composer.Focus()
	sessionID := "default"
	if len(initialSessionID) > 0 && initialSessionID[0] != "" {
		sessionID = initialSessionID[0]
	}
	return AppModel{
		runtime: runtime, initialCmd: focus, theme: theme, catalog: catalog, composer: composer, modelSearch: modelSearch,
		width: 80, height: 24, sessionID: sessionID, provider: provider, model: model,
		reasoning: reasoning, agentMode: mode, workspace: workspace, status: "Ready", approvalMode: ApprovalModePrompt,
		focus: focusComposer, transcriptCursor: -1, transcriptLayout: &transcriptLayoutCache{}, recapLayout: &recapLayoutCache{},
		auth: make(map[string]AuthView), modelsByProvider: make(map[string][]ModelChoice),
		reducedMotion: os.Getenv("AZEM_REDUCED_MOTION") == "1" || os.Getenv("REDUCED_MOTION") == "1",
	}
}

// SetLanguage changes only presentation strings; runtime and persisted states remain stable English values.
func (m *AppModel) SetLanguage(language string) error {
	catalog, err := i18n.New(language)
	if err != nil {
		return err
	}
	m.catalog = catalog
	m.composer.Placeholder = catalog.T("composer.placeholder")
	m.modelSearch.Prompt = catalog.T("search.prompt")
	m.modelSearch.Placeholder = catalog.T("search.placeholder")
	if m.transcriptLayout != nil {
		m.transcriptLayout.initialized = false
	}
	if m.recapLayout != nil {
		m.recapLayout.initialized = false
	}
	return nil
}

func (m AppModel) tr(key string, args ...map[string]string) string { return m.catalog.T(key, args...) }

func (m AppModel) displayState(state string) string {
	keys := map[string]string{
		"Ready": "status.ready", "Starting": "status.starting", "Running": "status.running",
		"Awaiting approval": "status.awaiting_approval", "Reviewing approval": "status.reviewing_approval",
		"Cancelling": "status.cancelling", "Failed": "status.failed", "Recovery attention": "status.recovery_attention",
		"completed": "status.completed", "denied": "status.denied", "streaming": "status.streaming",
		"failed": "status.failed", "running": "status.running",
	}
	if key := keys[state]; key != "" {
		return m.tr(key)
	}
	return state
}

func (m AppModel) Init() tea.Cmd {
	return tea.Batch(m.initialCmd, waitForAppEvent(m.runtime))
}

func (m *AppModel) openOverlay(overlay Overlay) {
	m.overlay = overlay
	m.overlayCursor = 0
	m.overlayScroll = 0
	m.focus = focusOverlay
	m.composer.Blur()
	m.modelSearch.Blur()
	if overlay == OverlayModel {
		m.modelSearch.Reset()
		_ = m.modelSearch.Focus()
		for index, entry := range m.modelPickerEntries() {
			if entry.Provider == m.provider && entry.Model.ID == m.model {
				m.overlayCursor = index
				break
			}
		}
	}
	if overlay == OverlayReasoning {
		for index, level := range m.reasoningLevels() {
			if level == m.reasoning {
				m.overlayCursor = index
				break
			}
		}
	}
	if overlay == OverlayLanguage {
		for index, language := range i18n.Languages() {
			if language == m.catalog.Language() {
				m.overlayCursor = index
				break
			}
		}
	}
}

func (m *AppModel) closeOverlay() tea.Cmd {
	if m.overlay == OverlayModel {
		m.modelSearch.Reset()
		m.modelSearch.Blur()
	}
	m.overlay = OverlayNone
	m.overlayCursor = 0
	m.overlayScroll = 0
	m.overlayPurpose = ""
	m.focus = focusComposer
	return m.composer.Focus()
}

func (m *AppModel) selectTranscript() bool {
	selectable := m.selectableTranscriptBlocks()
	if len(selectable) == 0 {
		return false
	}
	m.focus = focusTranscript
	m.composer.Blur()
	m.transcriptCursor = selectable[len(selectable)-1]
	return true
}

func (m AppModel) selectableTranscriptBlocks() []int {
	indices := make([]int, 0)
	for index, block := range m.transcript {
		if block.Kind == BlockTool || block.Kind == BlockDiff || block.Kind == BlockError {
			indices = append(indices, index)
		}
	}
	return indices
}

func (m *AppModel) moveTranscriptCursor(delta int) {
	indices := m.selectableTranscriptBlocks()
	if len(indices) == 0 {
		m.transcriptCursor = -1
		return
	}
	position := sort.SearchInts(indices, m.transcriptCursor)
	if position >= len(indices) || indices[position] != m.transcriptCursor {
		position = len(indices) - 1
	}
	position = (position + delta) % len(indices)
	if position < 0 {
		position += len(indices)
	}
	m.transcriptCursor = indices[position]
}

func (m AppModel) modelPickerEntries() []modelPickerEntry {
	providers := make([]string, 0, len(m.modelsByProvider))
	total := 0
	for provider, models := range m.modelsByProvider {
		if len(models) == 0 {
			continue
		}
		providers = append(providers, provider)
		total += len(models)
	}
	sort.Strings(providers)
	entries := make([]modelPickerEntry, 0, total)
	for _, provider := range providers {
		for _, model := range m.modelsByProvider[provider] {
			entries = append(entries, modelPickerEntry{Provider: provider, Model: model})
		}
	}
	query := strings.ToLower(strings.TrimSpace(m.modelSearch.Value()))
	if query == "" {
		return entries
	}
	filtered := entries[:0]
	for _, entry := range entries {
		searchable := strings.ToLower(strings.Join([]string{
			entry.Provider,
			entry.Model.ID,
			entry.Model.Name,
			entry.Model.Description,
			strings.Join(entry.Model.Aliases, " "),
		}, " "))
		matches := true
		for _, term := range strings.Fields(query) {
			if !strings.Contains(searchable, term) {
				matches = false
				break
			}
		}
		if matches {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func (m AppModel) modelCatalogCount() int {
	count := 0
	for _, models := range m.modelsByProvider {
		if len(models) > 0 {
			count++
		}
	}
	return count
}

func (m AppModel) selectedModelChoice() (ModelChoice, bool) {
	for _, choice := range m.models {
		if choice.ID == m.model {
			return choice, true
		}
	}
	return ModelChoice{}, false
}

func (m AppModel) reasoningLevels() []string {
	if choice, ok := m.selectedModelChoice(); ok {
		return catalog.AvailableReasoningLevels(m.provider, choice)
	}
	return catalog.AvailableReasoningLevels("", ModelChoice{SupportsReasoning: true})
}

func (m *AppModel) syncReasoningForModel() {
	choice, ok := m.selectedModelChoice()
	if !ok {
		return
	}
	levels := catalog.AvailableReasoningLevels(m.provider, choice)
	if contains(levels, m.reasoning) {
		return
	}
	m.reasoning = catalog.PreferredReasoningLevel(m.provider, choice)
}

var _ tea.Model = AppModel{}
