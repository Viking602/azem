package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

type PlanningQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Recommended bool   `json:"recommended,omitempty"`
}

type PlanningQuestion struct {
	ID            string                   `json:"id"`
	Header        string                   `json:"header"`
	Question      string                   `json:"question"`
	Options       []PlanningQuestionOption `json:"options"`
	AllowMultiple bool                     `json:"allow_multiple,omitempty"`
}

type PlanningAnswer struct {
	QuestionID string   `json:"question_id"`
	Selected   []string `json:"selected,omitempty"`
	Other      string   `json:"other,omitempty"`
}

type PlanningInputView struct {
	ID        string
	Questions []PlanningQuestion
	Index     int
	Answers   map[string]PlanningAnswer
}

type PlanReviewView struct {
	ID      string
	Title   string
	Body    string
	Version string
	State   string
}

func planningInputFromEvent(id, raw string) (*PlanningInputView, error) {
	var questions []PlanningQuestion
	if err := json.Unmarshal([]byte(raw), &questions); err != nil {
		return nil, fmt.Errorf("decode planning questions: %w", err)
	}
	if strings.TrimSpace(id) == "" || len(questions) == 0 {
		return nil, fmt.Errorf("planning question is incomplete")
	}
	return &PlanningInputView{ID: id, Questions: questions, Answers: make(map[string]PlanningAnswer)}, nil
}

func (input *PlanningInputView) current() (PlanningQuestion, bool) {
	if input == nil || input.Index < 0 || input.Index >= len(input.Questions) {
		return PlanningQuestion{}, false
	}
	return input.Questions[input.Index], true
}

func (input *PlanningInputView) toggle(label string) {
	question, ok := input.current()
	if !ok {
		return
	}
	answer := input.Answers[question.ID]
	answer.QuestionID = question.ID
	if !question.AllowMultiple {
		answer.Selected = []string{label}
	} else if slices.Contains(answer.Selected, label) {
		answer.Selected = slices.DeleteFunc(answer.Selected, func(value string) bool { return value == label })
	} else {
		answer.Selected = append(answer.Selected, label)
	}
	answer.Other = ""
	input.Answers[question.ID] = answer
}

func (input *PlanningInputView) setOther(value string) {
	question, ok := input.current()
	if !ok {
		return
	}
	input.Answers[question.ID] = PlanningAnswer{QuestionID: question.ID, Other: strings.TrimSpace(value)}
}

func (input *PlanningInputView) canConfirm() bool {
	question, ok := input.current()
	if !ok {
		return false
	}
	answer := input.Answers[question.ID]
	return len(answer.Selected) > 0 || strings.TrimSpace(answer.Other) != ""
}

func (input *PlanningInputView) advance() bool {
	if !input.canConfirm() {
		return false
	}
	input.Index++
	return input.Index >= len(input.Questions)
}

func (input *PlanningInputView) payload() json.RawMessage {
	answers := make([]PlanningAnswer, 0, len(input.Questions))
	for _, question := range input.Questions {
		answers = append(answers, input.Answers[question.ID])
	}
	payload, _ := json.Marshal(map[string]any{"answers": answers})
	return payload
}
