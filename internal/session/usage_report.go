package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	UsageReportWindowDays = 366
	UsageReportModelLimit = 20
	UsageReportSkillLimit = 20
	UsageScopeProject     = "project"
	UsageScopeAll         = "all"
)

// UsageReport is a bounded, secret-free ledger of completed provider requests.
// Cache counters follow PROVIDER-002: only cache_reported facts contribute
// reads; unknown providers stay unreported instead of being treated as zero.
type UsageReport struct {
	Scope              string          `json:"scope"`
	Workspace          string          `json:"workspace,omitempty"`
	From               string          `json:"from"`
	To                 string          `json:"to"`
	Empty              bool            `json:"empty"`
	Requests           int64           `json:"requests"`
	Sessions           int64           `json:"sessions"`
	Runs               int64           `json:"runs"`
	TotalTokens        int64           `json:"totalTokens"`
	InputTokens        int64           `json:"inputTokens"`
	OutputTokens       int64           `json:"outputTokens"`
	ReasoningTokens    int64           `json:"reasoningTokens"`
	ReportedInput      int64           `json:"reportedInputTokens"`
	CacheReadTokens    int64           `json:"cacheReadTokens"`
	CacheWriteTokens   int64           `json:"cacheWriteTokens"`
	CacheReported      bool            `json:"cacheReported"`
	CacheWriteReported bool            `json:"cacheWriteReported"`
	PeakDay            string          `json:"peakDay,omitempty"`
	PeakDayTokens      int64           `json:"peakDayTokens"`
	CurrentStreak      int             `json:"currentStreak"`
	LongestStreak      int             `json:"longestStreak"`
	LongestRunMS       int64           `json:"longestRunMs,omitempty"`
	Days               []UsageDay      `json:"days,omitempty"`
	Kinds              []UsageKindRow  `json:"kinds,omitempty"`
	Models             []UsageModelRow `json:"models,omitempty"`
	Skills             []UsageSkillRow `json:"skills,omitempty"`
}

type UsageDay struct {
	Date     string `json:"date"`
	Tokens   int64  `json:"tokens"`
	Requests int64  `json:"requests"`
}

type UsageKindRow struct {
	Kind     string `json:"kind"`
	Tokens   int64  `json:"tokens"`
	Requests int64  `json:"requests"`
}

type UsageModelRow struct {
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	Tokens             int64  `json:"tokens"`
	InputTokens        int64  `json:"inputTokens"`
	OutputTokens       int64  `json:"outputTokens"`
	CacheReadTokens    int64  `json:"cacheReadTokens"`
	CacheWriteTokens   int64  `json:"cacheWriteTokens"`
	CacheReported      bool   `json:"cacheReported"`
	CacheWriteReported bool   `json:"cacheWriteReported"`
	Requests           int64  `json:"requests"`
}

type UsageSkillRow struct {
	Name        string `json:"name"`
	Activations int64  `json:"activations"`
}

type UsageReportQuery struct {
	Scope     string
	Workspace string
	Now       time.Time
}

func (r UsageReport) Clone() UsageReport {
	cloned := r
	if r.Days != nil {
		cloned.Days = append([]UsageDay(nil), r.Days...)
	}
	if r.Kinds != nil {
		cloned.Kinds = append([]UsageKindRow(nil), r.Kinds...)
	}
	if r.Models != nil {
		cloned.Models = append([]UsageModelRow(nil), r.Models...)
	}
	if r.Skills != nil {
		cloned.Skills = append([]UsageSkillRow(nil), r.Skills...)
	}
	return cloned
}

func normalizeUsageScope(scope string) string {
	if strings.EqualFold(strings.TrimSpace(scope), UsageScopeAll) {
		return UsageScopeAll
	}
	return UsageScopeProject
}

func (s *Service) UsageReport(ctx context.Context, query UsageReportQuery) (UsageReport, error) {
	now := query.Now
	if now.IsZero() {
		now = time.Now()
	}
	scope := normalizeUsageScope(query.Scope)
	workspace := ""
	if scope == UsageScopeProject {
		workspace = strings.TrimSpace(query.Workspace)
	}
	from := now.AddDate(0, 0, -(UsageReportWindowDays - 1))
	loc := now.Location()
	fromDay := time.Date(from.In(loc).Year(), from.In(loc).Month(), from.In(loc).Day(), 0, 0, 0, 0, loc)
	toDay := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	report := UsageReport{
		Scope:     scope,
		Workspace: workspace,
		From:      fromDay.Format("2006-01-02"),
		To:        toDay.Format("2006-01-02"),
		Empty:     true,
	}
	if scope == UsageScopeProject && workspace == "" {
		return report, nil
	}
	if s == nil || s.db == nil {
		return report, nil
	}
	since := fromDay.UnixNano()
	totals, err := s.queryUsageTotals(ctx, since, workspace)
	if err != nil {
		return UsageReport{}, err
	}
	report.Requests = totals.requests
	report.Sessions = totals.sessions
	report.Runs = totals.runs
	report.TotalTokens = totals.total
	report.InputTokens = totals.input
	report.OutputTokens = totals.output
	report.ReasoningTokens = totals.reasoning
	report.ReportedInput = totals.reportedInput
	report.CacheReadTokens = totals.cacheRead
	report.CacheWriteTokens = totals.cacheWrite
	report.CacheReported = totals.cacheReported
	report.CacheWriteReported = totals.cacheWriteReported
	if totals.requests == 0 {
		return report, nil
	}
	report.Empty = false
	days, err := s.queryUsageDays(ctx, since, workspace)
	if err != nil {
		return UsageReport{}, err
	}
	report.Days = days
	report.PeakDay, report.PeakDayTokens, report.CurrentStreak, report.LongestStreak = summarizeUsageDays(days, toDay.Format("2006-01-02"))
	kinds, err := s.queryUsageKinds(ctx, since, workspace)
	if err != nil {
		return UsageReport{}, err
	}
	report.Kinds = kinds
	models, err := s.queryUsageModels(ctx, since, workspace)
	if err != nil {
		return UsageReport{}, err
	}
	report.Models = models
	skills, err := s.queryUsageSkills(ctx, since, workspace)
	if err != nil {
		return UsageReport{}, err
	}
	report.Skills = skills
	longest, err := s.queryLongestRunMS(ctx, since, workspace)
	if err != nil {
		return UsageReport{}, err
	}
	report.LongestRunMS = longest
	return report, nil
}

type usageTotals struct {
	requests, sessions, runs, total, input, output, reasoning, reportedInput, cacheRead, cacheWrite int64
	cacheReported, cacheWriteReported                                                               bool
}

func usageScopeArgs(since int64, workspace string) []any {
	return []any{since, workspace, workspace}
}

const usageScopeSQL = `status='completed' AND completed_at>=? AND (?='' OR EXISTS (
	SELECT 1 FROM session_workspaces sw WHERE sw.session_id=provider_requests.session_id AND sw.workspace=?
))`

const usageTokenSQL = `CASE WHEN total_tokens>0 THEN total_tokens ELSE input_tokens+output_tokens END`

func (s *Service) queryUsageTotals(ctx context.Context, since int64, workspace string) (usageTotals, error) {
	var row usageTotals
	var cacheReported, cacheWriteReported int64
	err := s.db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COUNT(DISTINCT session_id),
		COUNT(DISTINCT CASE WHEN run_id<>'' THEN run_id END),
		CAST(COALESCE(SUM(`+usageTokenSQL+`),0) AS INTEGER),
		CAST(COALESCE(SUM(input_tokens),0) AS INTEGER),
		CAST(COALESCE(SUM(output_tokens),0) AS INTEGER),
		CAST(COALESCE(SUM(reasoning_tokens),0) AS INTEGER),
		CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN input_tokens ELSE 0 END),0) AS INTEGER),
		CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN cached_tokens ELSE 0 END),0) AS INTEGER),
		CAST(COALESCE(SUM(CASE WHEN cache_write_reported=1 THEN cache_write_tokens ELSE 0 END),0) AS INTEGER),
		CAST(COALESCE(MAX(cache_reported),0) AS INTEGER),
		CAST(COALESCE(MAX(cache_write_reported),0) AS INTEGER)
	FROM provider_requests WHERE `+usageScopeSQL, usageScopeArgs(since, workspace)...).Scan(
		&row.requests, &row.sessions, &row.runs, &row.total, &row.input, &row.output, &row.reasoning,
		&row.reportedInput, &row.cacheRead, &row.cacheWrite, &cacheReported, &cacheWriteReported,
	)
	if err != nil {
		return usageTotals{}, fmt.Errorf("usage totals: %w", err)
	}
	row.cacheReported = cacheReported != 0
	row.cacheWriteReported = cacheWriteReported != 0
	return row, nil
}

func (s *Service) queryUsageDays(ctx context.Context, since int64, workspace string) ([]UsageDay, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		strftime('%Y-%m-%d', completed_at/1000000000, 'unixepoch', 'localtime'),
		CAST(COALESCE(SUM(`+usageTokenSQL+`),0) AS INTEGER),
		COUNT(*)
	FROM provider_requests WHERE `+usageScopeSQL+`
	GROUP BY 1 ORDER BY 1`, usageScopeArgs(since, workspace)...)
	if err != nil {
		return nil, fmt.Errorf("usage days: %w", err)
	}
	defer rows.Close()
	var days []UsageDay
	for rows.Next() {
		var day UsageDay
		if err := rows.Scan(&day.Date, &day.Tokens, &day.Requests); err != nil {
			return nil, err
		}
		if day.Date != "" {
			days = append(days, day)
		}
	}
	return days, rows.Err()
}

func (s *Service) queryUsageKinds(ctx context.Context, since int64, workspace string) ([]UsageKindRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT request_kind,
		CAST(COALESCE(SUM(`+usageTokenSQL+`),0) AS INTEGER),
		COUNT(*)
	FROM provider_requests WHERE `+usageScopeSQL+`
	GROUP BY request_kind
	ORDER BY 2 DESC, 3 DESC, request_kind`, usageScopeArgs(since, workspace)...)
	if err != nil {
		return nil, fmt.Errorf("usage kinds: %w", err)
	}
	defer rows.Close()
	var kinds []UsageKindRow
	for rows.Next() {
		var row UsageKindRow
		if err := rows.Scan(&row.Kind, &row.Tokens, &row.Requests); err != nil {
			return nil, err
		}
		if strings.TrimSpace(row.Kind) != "" {
			kinds = append(kinds, row)
		}
	}
	return kinds, rows.Err()
}

func (s *Service) queryUsageModels(ctx context.Context, since int64, workspace string) ([]UsageModelRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT provider, model,
		CAST(COALESCE(SUM(`+usageTokenSQL+`),0) AS INTEGER),
		CAST(COALESCE(SUM(input_tokens),0) AS INTEGER),
		CAST(COALESCE(SUM(output_tokens),0) AS INTEGER),
		CAST(COALESCE(SUM(CASE WHEN cache_reported=1 THEN cached_tokens ELSE 0 END),0) AS INTEGER),
		CAST(COALESCE(SUM(CASE WHEN cache_write_reported=1 THEN cache_write_tokens ELSE 0 END),0) AS INTEGER),
		CAST(COALESCE(MAX(cache_reported),0) AS INTEGER),
		CAST(COALESCE(MAX(cache_write_reported),0) AS INTEGER),
		COUNT(*)
	FROM provider_requests WHERE `+usageScopeSQL+`
	GROUP BY provider, model
	ORDER BY 3 DESC, 10 DESC, provider, model
	LIMIT ?`, append(usageScopeArgs(since, workspace), UsageReportModelLimit)...)
	if err != nil {
		return nil, fmt.Errorf("usage models: %w", err)
	}
	defer rows.Close()
	var models []UsageModelRow
	for rows.Next() {
		var row UsageModelRow
		var cacheReported, cacheWriteReported int64
		if err := rows.Scan(&row.Provider, &row.Model, &row.Tokens, &row.InputTokens, &row.OutputTokens, &row.CacheReadTokens, &row.CacheWriteTokens, &cacheReported, &cacheWriteReported, &row.Requests); err != nil {
			return nil, err
		}
		row.CacheReported = cacheReported != 0
		row.CacheWriteReported = cacheWriteReported != 0
		models = append(models, row)
	}
	return models, rows.Err()
}

func (s *Service) queryUsageSkills(ctx context.Context, since int64, workspace string) ([]UsageSkillRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		COALESCE(NULLIF(TRIM(json_extract(arguments,'$.name')),''), NULLIF(TRIM(json_extract(arguments,'$.skill')),'')) AS skill,
		COUNT(*)
	FROM session_tool_records
	WHERE name='hydaelyn_activate_skill' AND state='completed' AND started_at>=?
		AND (?='' OR EXISTS (
			SELECT 1 FROM session_workspaces sw WHERE sw.session_id=session_tool_records.session_id AND sw.workspace=?
		))
	GROUP BY skill
	HAVING skill IS NOT NULL AND skill<>''
	ORDER BY 2 DESC, skill
	LIMIT ?`, since, workspace, workspace, UsageReportSkillLimit)
	if err != nil {
		return nil, fmt.Errorf("usage skills: %w", err)
	}
	defer rows.Close()
	var skills []UsageSkillRow
	for rows.Next() {
		var row UsageSkillRow
		if err := rows.Scan(&row.Name, &row.Activations); err != nil {
			return nil, err
		}
		skills = append(skills, row)
	}
	return skills, rows.Err()
}

func (s *Service) queryLongestRunMS(ctx context.Context, since int64, workspace string) (int64, error) {
	var duration int64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(end_ns-start_ns)
	FROM (
		SELECT MIN(started_at) AS start_ns,
			MAX(CASE WHEN completed_at>0 THEN completed_at ELSE started_at END) AS end_ns
		FROM provider_requests
		WHERE `+usageScopeSQL+` AND run_id<>''
		GROUP BY session_id, run_id
	)`, usageScopeArgs(since, workspace)...).Scan(&duration)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("usage longest run: %w", err)
	}
	if duration <= 0 {
		return 0, nil
	}
	return duration / int64(time.Millisecond), nil
}

func summarizeUsageDays(days []UsageDay, today string) (peakDay string, peakTokens int64, current, longest int) {
	active := make(map[string]int64, len(days))
	for _, day := range days {
		if day.Tokens <= 0 && day.Requests <= 0 {
			continue
		}
		active[day.Date] = day.Tokens
		if day.Tokens > peakTokens {
			peakTokens = day.Tokens
			peakDay = day.Date
		}
	}
	if len(active) == 0 {
		return "", 0, 0, 0
	}
	parsed, err := time.ParseInLocation("2006-01-02", today, time.Local)
	if err != nil {
		return peakDay, peakTokens, 0, 0
	}
	for cursor, streak := parsed, 0; ; cursor = cursor.AddDate(0, 0, -1) {
		if _, ok := active[cursor.Format("2006-01-02")]; !ok {
			break
		}
		streak++
		current = streak
	}
	for date := range active {
		start, parseErr := time.ParseInLocation("2006-01-02", date, time.Local)
		if parseErr != nil {
			continue
		}
		if _, ok := active[start.AddDate(0, 0, -1).Format("2006-01-02")]; ok {
			continue
		}
		streak := 0
		for cursor := start; ; cursor = cursor.AddDate(0, 0, 1) {
			if _, ok := active[cursor.Format("2006-01-02")]; !ok {
				break
			}
			streak++
		}
		if streak > longest {
			longest = streak
		}
	}
	return peakDay, peakTokens, current, longest
}
