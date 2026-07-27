package app

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/go-hydaelyn/message"

	hyprovider "github.com/Viking602/go-hydaelyn/provider"
	hyskill "github.com/Viking602/go-hydaelyn/skill"
)

func TestContextProfileFromRequestClassifiesEveryWireContribution(t *testing.T) {
	core := message.NewText(message.RoleSystem, "core instructions")
	skillMessage := message.NewText(message.RoleSystem, hyskill.RenderSystemSection([]hyskill.Skill{{
		Name: "demo", Description: "Demo skill", SourceDir: "/skills/demo", Body: "Follow the demo workflow.",
	}}))
	skillMessage.Metadata = map[string]string{skillContextMetadataKey: "active"}
	user := message.NewText(message.RoleUser, "current user request")

	profile := contextProfileFromRequest(hyprovider.Request{
		Messages: []message.Message{core, skillMessage, user},
		Tools: []message.ToolDefinition{
			{Name: "read", Description: "Read a file", InputSchema: message.JSONSchema{Type: "object"}},
			{Name: "mcp__grep__search", Description: "Search", InputSchema: message.JSONSchema{Type: "object"}, Origin: "mcp:grep"},
		},
	})

	if profile.Source != "request" || !profile.Estimated || profile.TotalTokens() <= 0 {
		t.Fatalf("profile metadata = source:%q estimated:%v total:%d", profile.Source, profile.Estimated, profile.TotalTokens())
	}
	want := map[ContextCategory]map[string]bool{
		ContextCategoryCore:         {"azem.core_instructions": true},
		ContextCategorySkills:       {"demo": true},
		ContextCategoryBuiltinTools: {"read": true},
		ContextCategoryMCP:          {"mcp__grep__search": true},
		ContextCategoryConversation: {"message:user:1": true},
	}
	for _, contribution := range profile.Contributions {
		if names := want[contribution.Category]; names != nil {
			delete(names, contribution.Name)
		}
		if contribution.Tokens <= 0 {
			t.Fatalf("non-positive contribution: %+v", contribution)
		}
	}
	for category, names := range want {
		if len(names) != 0 {
			t.Fatalf("missing %s contributions: %v; profile=%+v", category, names, profile.Contributions)
		}
	}
}

func TestContextProfileTotalIgnoresNegativeContributions(t *testing.T) {
	profile := ContextProfile{Contributions: []ContextContribution{
		{Category: ContextCategoryCore, Name: "core", Tokens: 12},
		{Category: ContextCategoryOther, Name: "invalid", Tokens: -5},
	}}
	if got := profile.TotalTokens(); got != 12 {
		t.Fatalf("TotalTokens() = %d, want 12", got)
	}
}

func TestContextProfileTotalSaturatesAtMaxInt(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	profile := ContextProfile{Contributions: []ContextContribution{
		{Category: ContextCategoryCore, Name: "core", Tokens: maxInt},
		{Category: ContextCategoryOther, Name: "overflow", Tokens: 1},
	}}
	if got := profile.TotalTokens(); got != maxInt {
		t.Fatalf("TotalTokens() = %d, want %d", got, maxInt)
	}
}

func TestContextContributionsAggregateAfterCategoryLimit(t *testing.T) {
	var contributions []ContextContribution
	for range maxContextContributionsPerCategory + 10 {
		contributions = appendTokenContribution(contributions, ContextCategoryMCP, "tool", 1)
	}
	if got, want := len(contributions), maxContextContributionsPerCategory+1; got != want {
		t.Fatalf("contribution count = %d, want %d", got, want)
	}
	profile := ContextProfile{Contributions: contributions}
	if got, want := profile.TotalTokens(), maxContextContributionsPerCategory+10; got != want {
		t.Fatalf("TotalTokens() = %d, want %d", got, want)
	}
	if got := contributions[len(contributions)-1]; got.Name != ContextContributionRemainingItems || got.Tokens != 10 {
		t.Fatalf("remainder = %+v", got)
	}
}

func TestContextContributionNamesAreBoundedAndValid(t *testing.T) {
	name := strings.Repeat("x", maxContextContributionNameBytes-2) + "\xff" + strings.Repeat("y", 10)
	contributions := appendTokenContribution(nil, ContextCategoryMCP, name, 1)
	if got := contributions[0].Name; len(got) > maxContextContributionNameBytes || !utf8.ValidString(got) {
		t.Fatalf("bounded contribution name = %q (%d bytes)", got, len(got))
	}
}

func TestToolEstimateBoundsRecursiveSchemas(t *testing.T) {
	schema := message.JSONSchema{Type: "object"}
	schema.Items = &schema
	tokens := estimateToolTokens(message.ToolDefinition{Name: "recursive", InputSchema: schema})
	if got, want := tokens, estimateByteTokens(maxDefinitionEstimateBytes); got != want {
		t.Fatalf("estimateToolTokens() = %d, want bounded %d", got, want)
	}
}

func TestSkillRuntimeToolDefinitionsMatchInjectedCapabilities(t *testing.T) {
	definitions := skillRuntimeToolDefinitions(
		[]hyskill.Skill{{Name: "active", Resources: []hyskill.Resource{{Name: "guide.txt"}}}},
		[]hyskill.Skill{{Name: "available"}},
	)
	if len(definitions) != 2 {
		t.Fatalf("skill runtime definitions = %d, want 2", len(definitions))
	}
	if definitions[0].Name != "hydaelyn_activate_skill" || definitions[1].Name != "hydaelyn_read_skill_resource" {
		t.Fatalf("skill runtime definitions = %q, %q", definitions[0].Name, definitions[1].Name)
	}
}

func TestTeamUsageDriverEmitsPreparedAndReportedProfiles(t *testing.T) {
	var profiles []ContextProfile
	driver := &teamUsageDriver{
		inner: aggregateOnlyMeteringDriver{},
		emitProfile: func(profile ContextProfile) {
			profiles = append(profiles, profile)
		},
	}
	stream, err := driver.Stream(context.Background(), hyprovider.Request{
		Messages: []message.Message{message.NewText(message.RoleUser, "team request")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profile events = %d, want 2", len(profiles))
	}
	if profiles[0].Source != "team_request" || profiles[0].ReportedInputTokens != 0 {
		t.Fatalf("prepared profile = %+v", profiles[0])
	}
	if profiles[1].ReportedInputTokens != 12 || profiles[1].ReportedOutputTokens != 3 {
		t.Fatalf("reported profile = %+v", profiles[1])
	}
}

func TestProjectionContextMessagesAppendUncoveredTail(t *testing.T) {
	covered := int64(1)
	projection := session.Projection{
		ModelHistory: session.ModelHistory{
			Messages:               []message.Message{message.NewText(message.RoleUser, "saved")},
			CoveredThroughSequence: &covered,
		},
		Blocks: []session.Block{
			{Sequence: 1, Kind: "user", Content: "covered"},
			{Sequence: 2, Kind: "assistant", Content: "uncovered"},
		},
	}
	messages := projectionContextMessages(projection)
	if len(messages) != 2 || messages[0].Text != "saved" || messages[1].Text != "uncovered" {
		t.Fatalf("projection context messages = %+v", messages)
	}
}

func TestProjectionContextMessagesIgnoreCheckpointWithoutBoundary(t *testing.T) {
	projection := session.Projection{
		ModelHistory: session.ModelHistory{Messages: []message.Message{message.NewText(message.RoleUser, "stale")}},
		Blocks:       []session.Block{{Sequence: 1, Kind: "user", Content: "canonical"}},
	}
	messages := projectionContextMessages(projection)
	if len(messages) != 1 || messages[0].Text != "canonical" {
		t.Fatalf("projection context messages = %+v", messages)
	}
}
