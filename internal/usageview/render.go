package usageview

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func Text(report session.UsageReport) string {
	var output strings.Builder
	title := "All projects"
	if report.Scope == session.UsageScopeProject {
		title = report.Workspace
		if strings.TrimSpace(title) == "" {
			title = "Current project"
		}
	}
	fmt.Fprintf(&output, "Usage · %s\n", title)
	fmt.Fprintf(&output, "Period: %s → %s\n\n", report.From, report.To)
	if report.Empty {
		output.WriteString("No completed provider requests in this period.\n")
		return output.String()
	}
	fmt.Fprintf(&output, "Requests: %s · Sessions: %s · Runs: %s\n", integer(report.Requests), integer(report.Sessions), integer(report.Runs))
	fmt.Fprintf(&output, "Tokens: %s total · %s input · %s output · %s reasoning\n", compact(report.TotalTokens), compact(report.InputTokens), compact(report.OutputTokens), compact(report.ReasoningTokens))
	if report.CacheReported && report.ReportedInput > 0 {
		fmt.Fprintf(&output, "Cache reads: %s / %s reported input (%.1f%%)\n", compact(report.CacheReadTokens), compact(report.ReportedInput), 100*float64(report.CacheReadTokens)/float64(report.ReportedInput))
	} else {
		output.WriteString("Cache reads: not reported\n")
	}
	if report.PeakDay != "" {
		fmt.Fprintf(&output, "Peak day: %s (%s tokens) · Streak: %d current / %d longest\n", report.PeakDay, compact(report.PeakDayTokens), report.CurrentStreak, report.LongestStreak)
	}
	if report.LongestRunMS > 0 {
		fmt.Fprintf(&output, "Longest run: %s\n", (time.Duration(report.LongestRunMS) * time.Millisecond).Round(time.Second))
	}
	output.WriteString("\nDaily activity (oldest → newest)\n")
	output.WriteString(heatmap(report))
	if len(report.Models) > 0 {
		output.WriteString("\nModels\n")
		for _, model := range report.Models {
			fmt.Fprintf(&output, "  %-24s %10s tokens  %6s requests\n", truncate(model.Provider+"/"+model.Model, 24), compact(model.Tokens), integer(model.Requests))
		}
	}
	if len(report.Kinds) > 0 {
		output.WriteString("\nRequest kinds\n")
		for _, kind := range report.Kinds {
			fmt.Fprintf(&output, "  %-16s %10s tokens  %6s requests\n", truncate(kind.Kind, 16), compact(kind.Tokens), integer(kind.Requests))
		}
	}
	if len(report.Skills) > 0 {
		output.WriteString("\nSkills\n")
		for _, skill := range report.Skills {
			fmt.Fprintf(&output, "  %-24s %s activations\n", truncate(skill.Name, 24), integer(skill.Activations))
		}
	}
	return output.String()
}

func heatmap(report session.UsageReport) string {
	days := report.Days
	if len(days) == 0 {
		return "  no activity\n"
	}
	byDate := make(map[string]session.UsageDay, len(days))
	for _, day := range days {
		byDate[day.Date] = day
	}
	from, fromErr := time.Parse("2006-01-02", report.From)
	to, toErr := time.Parse("2006-01-02", report.To)
	values := make([]session.UsageDay, 0, 366)
	if fromErr == nil && toErr == nil && !to.Before(from) {
		for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
			date := day.Format("2006-01-02")
			value := byDate[date]
			value.Date = date
			values = append(values, value)
		}
	} else {
		values = append(values, days...)
		sort.Slice(values, func(left, right int) bool { return values[left].Date < values[right].Date })
	}
	peak := int64(1)
	for _, day := range values {
		if day.Tokens > peak {
			peak = day.Tokens
		}
	}
	const shades = " .:-=+*#%@"
	var output strings.Builder
	for start := 0; start < len(values); start += 52 {
		end := min(start+52, len(values))
		fmt.Fprintf(&output, "  %s  ", values[start].Date)
		for _, day := range values[start:end] {
			index := int(float64(day.Tokens) / float64(peak) * float64(len(shades)-1))
			if day.Tokens > 0 && index == 0 {
				index = 1
			}
			output.WriteByte(shades[index])
		}
		fmt.Fprintf(&output, "  %s\n", values[end-1].Date)
	}
	return output.String()
}

func integer(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}
	digits := fmt.Sprint(value)
	for index := len(digits) - 3; index > 0; index -= 3 {
		digits = digits[:index] + "," + digits[index:]
	}
	if negative {
		return "-" + digits
	}
	return digits
}

func compact(value int64) string {
	switch {
	case value >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(value)/1_000_000_000)
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return integer(value)
	}
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}
