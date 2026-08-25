package benchmark

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Profile string

const (
	ProfileMix        Profile = "mix"
	ProfileChat       Profile = "chat"
	ProfilePrefill    Profile = "prefill"
	ProfileGeneration Profile = "generation"
)

type Query struct {
	Model      string
	Provider   string
	Profile    Profile
	Prompt     string
	MaxTokens  int
	InputBytes int
	PairID     int
	Warm       bool
}

type Observation struct {
	Model         string        `json:"model"`
	Provider      string        `json:"provider,omitempty"`
	Profile       Profile       `json:"profile"`
	Run           int           `json:"run"`
	PairID        int           `json:"pairId,omitempty"`
	Warm          bool          `json:"warm,omitempty"`
	Latency       time.Duration `json:"latency"`
	TimeToFirst   time.Duration `json:"timeToFirst"`
	InputTokens   int64         `json:"inputTokens"`
	OutputTokens  int64         `json:"outputTokens"`
	CachedTokens  int64         `json:"cachedTokens"`
	CacheReported bool          `json:"cacheReported"`
	OutputBytes   int64         `json:"outputBytes"`
	Error         string        `json:"error,omitempty"`
}

type Executor interface {
	Execute(ctx context.Context, query Query) Observation
}

type Options struct {
	Models           []string
	Profile          Profile
	Runs             int
	Parallel         int
	MaxTokens        int
	Prompt           string
	PrefillBytes     int
	Cache            bool
	CachePairs       int
	CacheConcurrency int
}

type ModelReport struct {
	Model             string          `json:"model"`
	Runs              int             `json:"runs"`
	Succeeded         int             `json:"succeeded"`
	Failed            int             `json:"failed"`
	LatencyMedian     time.Duration   `json:"latencyMedian"`
	LatencyP95        time.Duration   `json:"latencyP95"`
	TimeToFirstMedian time.Duration   `json:"timeToFirstMedian"`
	TokensPerSecond   float64         `json:"tokensPerSecond"`
	InputTokens       int64           `json:"inputTokens"`
	OutputTokens      int64           `json:"outputTokens"`
	CachedTokens      int64           `json:"cachedTokens"`
	CacheReported     bool            `json:"cacheReported"`
	WarmSpeedup       float64         `json:"warmSpeedup,omitempty"`
	Profiles          map[Profile]int `json:"profiles"`
}

type Report struct {
	StartedAt    time.Time     `json:"startedAt"`
	FinishedAt   time.Time     `json:"finishedAt"`
	Observations []Observation `json:"observations"`
	Models       []ModelReport `json:"models"`
}

type Manager struct {
	executor Executor
	now      func() time.Time
}

func New(executor Executor) (*Manager, error) {
	if executor == nil {
		return nil, errors.New("benchmark executor is required")
	}
	return &Manager{executor: executor, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (manager *Manager) Run(ctx context.Context, options Options) (Report, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return Report{}, err
	}
	report := Report{StartedAt: manager.now()}
	queries := buildQueries(options)
	batches := queryBatches(queries, options.Cache)
	jobs := make(chan []indexedQuery)
	results := make(chan indexedObservation, len(queries))
	workers := options.Parallel
	if options.Cache {
		workers = options.CacheConcurrency
	}
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for batch := range jobs {
				for _, job := range batch {
					observation := manager.executor.Execute(ctx, job.query)
					observation.Model, observation.Provider, observation.Profile = job.query.Model, job.query.Provider, job.query.Profile
					observation.Run, observation.PairID, observation.Warm = job.run, job.query.PairID, job.query.Warm
					results <- indexedObservation{index: job.index, observation: observation}
					if ctx.Err() != nil {
						return
					}
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, batch := range batches {
			select {
			case jobs <- batch:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { group.Wait(); close(results) }()
	indexed := make([]indexedObservation, 0, len(queries))
	for result := range results {
		indexed = append(indexed, result)
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	sort.Slice(indexed, func(left, right int) bool { return indexed[left].index < indexed[right].index })
	report.Observations = make([]Observation, len(indexed))
	for index, value := range indexed {
		report.Observations[index] = value.observation
	}
	report.Models = summarize(report.Observations)
	report.FinishedAt = manager.now()
	return report, nil
}

type indexedQuery struct {
	index int
	run   int
	query Query
}

type indexedObservation struct {
	index       int
	observation Observation
}

func queryBatches(queries []indexedQuery, cache bool) [][]indexedQuery {
	if !cache {
		batches := make([][]indexedQuery, len(queries))
		for index := range queries {
			batches[index] = []indexedQuery{queries[index]}
		}
		return batches
	}
	batches := make([][]indexedQuery, 0, (len(queries)+1)/2)
	for index := 0; index < len(queries); index += 2 {
		end := min(index+2, len(queries))
		batches = append(batches, append([]indexedQuery(nil), queries[index:end]...))
	}
	return batches
}

func normalizeOptions(options Options) (Options, error) {
	models := make([]string, 0, len(options.Models))
	seen := make(map[string]struct{})
	for _, model := range options.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, duplicate := seen[model]; duplicate {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	if len(models) == 0 {
		return options, errors.New("benchmark requires at least one model")
	}
	options.Models = models
	if options.Profile == "" {
		options.Profile = ProfileMix
	}
	if options.Profile != ProfileMix && options.Profile != ProfileChat && options.Profile != ProfilePrefill && options.Profile != ProfileGeneration {
		return options, fmt.Errorf("unsupported benchmark profile %q", options.Profile)
	}
	if options.Runs <= 0 {
		options.Runs = 9
		if options.Profile == ProfileChat {
			options.Runs = 10
		} else if options.Profile == ProfilePrefill || options.Profile == ProfileGeneration {
			options.Runs = 5
		}
	}
	if options.Runs > 1000 {
		return options, errors.New("benchmark runs cannot exceed 1000 per model")
	}
	if options.Parallel <= 0 {
		options.Parallel = 4
	}
	if options.Parallel > 32 {
		return options, errors.New("benchmark parallelism cannot exceed 32")
	}
	if options.PrefillBytes <= 0 {
		options.PrefillBytes = 32 << 10
	}
	if options.PrefillBytes > 8<<20 {
		return options, errors.New("benchmark prefill bytes cannot exceed 8 MiB")
	}
	if options.CachePairs <= 0 {
		options.CachePairs = 1
	}
	if options.CacheConcurrency <= 0 {
		options.CacheConcurrency = 1
	}
	if options.CacheConcurrency > 16 {
		return options, errors.New("benchmark cache concurrency cannot exceed 16")
	}
	return options, nil
}

func buildQueries(options Options) []indexedQuery {
	queries := make([]indexedQuery, 0)
	index := 0
	for _, selector := range options.Models {
		provider, model := splitSelector(selector)
		if options.Cache {
			for pair := 1; pair <= options.CachePairs; pair++ {
				prompt := stableCachePrompt(options, pair)
				for _, warm := range []bool{false, true} {
					queries = append(queries, indexedQuery{index: index, run: pair, query: Query{Model: model, Provider: provider, Profile: ProfilePrefill, Prompt: prompt, MaxTokens: defaultMaxTokens(options, ProfilePrefill), InputBytes: options.PrefillBytes, PairID: pair, Warm: warm}})
					index++
				}
			}
			continue
		}
		for run := 1; run <= options.Runs; run++ {
			profile := options.Profile
			if profile == ProfileMix {
				profile = []Profile{ProfileChat, ProfilePrefill, ProfileGeneration}[(run-1)%3]
			}
			queries = append(queries, indexedQuery{index: index, run: run, query: Query{Model: model, Provider: provider, Profile: profile, Prompt: promptFor(options, profile, run), MaxTokens: defaultMaxTokens(options, profile), InputBytes: inputBytes(options, profile)}})
			index++
		}
	}
	return queries
}

func splitSelector(value string) (string, string) {
	provider, model, found := strings.Cut(value, "/")
	if !found {
		return "", value
	}
	return provider, model
}

func promptFor(options Options, profile Profile, run int) string {
	if strings.TrimSpace(options.Prompt) != "" {
		return options.Prompt
	}
	switch profile {
	case ProfilePrefill:
		return syntheticPrefix(options.PrefillBytes, run) + "\nReply with the word READY."
	case ProfileGeneration:
		return "Write a detailed, numbered technical explanation of deterministic distributed systems. Continue until the response limit."
	default:
		return "Explain one practical way to make a concurrent program easier to debug."
	}
}

func stableCachePrompt(options Options, pair int) string {
	return syntheticPrefix(options.PrefillBytes, pair) + "\nSummarize this stable prefix in one sentence."
}

func syntheticPrefix(bytes, seed int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var output strings.Builder
	output.Grow(bytes)
	for index := 0; index < bytes; index++ {
		output.WriteByte(alphabet[(index+seed)%len(alphabet)])
	}
	return output.String()
}

func defaultMaxTokens(options Options, profile Profile) int {
	if options.MaxTokens > 0 {
		return options.MaxTokens
	}
	switch profile {
	case ProfilePrefill:
		return 64
	case ProfileGeneration:
		return 2048
	default:
		return 512
	}
}

func inputBytes(options Options, profile Profile) int {
	if profile == ProfilePrefill {
		return options.PrefillBytes
	}
	return 0
}

func summarize(observations []Observation) []ModelReport {
	groups := make(map[string][]Observation)
	for _, observation := range observations {
		key := observation.Provider + "\x00" + observation.Model
		groups[key] = append(groups[key], observation)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	reports := make([]ModelReport, 0, len(keys))
	for _, key := range keys {
		values := groups[key]
		report := ModelReport{Model: values[0].Model, Runs: len(values), Profiles: make(map[Profile]int)}
		latencies, firsts := make([]time.Duration, 0), make([]time.Duration, 0)
		var successfulDuration time.Duration
		var cold, warm []time.Duration
		for _, value := range values {
			report.Profiles[value.Profile]++
			if value.Error != "" {
				report.Failed++
				continue
			}
			report.Succeeded++
			latencies, firsts = append(latencies, value.Latency), append(firsts, value.TimeToFirst)
			report.InputTokens += value.InputTokens
			report.OutputTokens += value.OutputTokens
			report.CachedTokens += value.CachedTokens
			report.CacheReported = report.CacheReported || value.CacheReported
			successfulDuration += value.Latency
			if value.PairID > 0 {
				if value.Warm {
					warm = append(warm, value.Latency)
				} else {
					cold = append(cold, value.Latency)
				}
			}
		}
		report.LatencyMedian, report.LatencyP95, report.TimeToFirstMedian = percentile(latencies, .5), percentile(latencies, .95), percentile(firsts, .5)
		if successfulDuration > 0 {
			report.TokensPerSecond = float64(report.OutputTokens) / successfulDuration.Seconds()
		}
		if len(cold) > 0 && len(warm) > 0 {
			coldMedian, warmMedian := percentile(cold, .5), percentile(warm, .5)
			if warmMedian > 0 {
				report.WarmSpeedup = float64(coldMedian) / float64(warmMedian)
			}
		}
		reports = append(reports, report)
	}
	return reports
}

func percentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	values = append([]time.Duration(nil), values...)
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
	index := int(float64(len(values)-1)*quantile + .5)
	return values[min(index, len(values)-1)]
}
