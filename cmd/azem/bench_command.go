package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/benchmark"
	"github.com/Viking602/azem/internal/operator"
)

func benchCommand(ctx context.Context, args []string, streams operator.IO) error {
	flags := flag.NewFlagSet("bench", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	gatewayURL := flags.String("gateway", "http://127.0.0.1:4000", "Azem auth gateway URL")
	token := flags.String("token", firstNonempty(os.Getenv("AZEM_AUTH_GATEWAY_TOKEN"), os.Getenv("OMP_AUTH_GATEWAY_TOKEN")), "gateway bearer token")
	modelsFlag := flags.String("models", "", "comma-separated provider/model selectors")
	profile := flags.String("profile", "mix", "mix, chat, prefill, or generation")
	runs := flags.Int("runs", 0, "runs per model")
	parallel := flags.Int("parallel", 4, "parallel requests")
	maxTokens := flags.Int("max-tokens", 0, "maximum output tokens")
	prompt := flags.String("prompt", "", "custom prompt")
	prefillBytes := flags.Int("prefill-bytes", 32<<10, "synthetic prefill bytes")
	cache := flags.Bool("cache", false, "run sequential cold/warm cache pairs")
	cachePairs := flags.Int("cache-pairs", 1, "cache pairs per model")
	cacheConcurrency := flags.Int("cache-concurrency", 1, "concurrent cache pairs")
	timeout := flags.Duration("timeout", 10*time.Minute, "overall benchmark timeout")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	models := append([]string(nil), flags.Args()...)
	if strings.TrimSpace(*modelsFlag) != "" {
		models = append(models, strings.Split(*modelsFlag, ",")...)
	}
	if len(models) == 0 {
		return errors.New("bench requires model selectors as arguments or --models")
	}
	executor, err := benchmark.NewHTTPExecutor(benchmark.HTTPExecutorOptions{GatewayURL: *gatewayURL, Token: *token})
	if err != nil {
		return err
	}
	manager, _ := benchmark.New(executor)
	runCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	report, err := manager.Run(runCtx, benchmark.Options{
		Models: models, Profile: benchmark.Profile(*profile), Runs: *runs, Parallel: *parallel, MaxTokens: *maxTokens,
		Prompt: *prompt, PrefillBytes: *prefillBytes, Cache: *cache, CachePairs: *cachePairs, CacheConcurrency: *cacheConcurrency,
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(streams.Out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	fmt.Fprintf(streams.Out, "Benchmark %s → %s\n", report.StartedAt.Format(time.RFC3339), report.FinishedAt.Format(time.RFC3339))
	for _, model := range report.Models {
		fmt.Fprintf(streams.Out, "%-28s %3d/%-3d ok  p50 %8s  p95 %8s  first %8s  %7.1f tok/s", model.Model, model.Succeeded, model.Runs, model.LatencyMedian.Round(time.Millisecond), model.LatencyP95.Round(time.Millisecond), model.TimeToFirstMedian.Round(time.Millisecond), model.TokensPerSecond)
		if model.WarmSpeedup > 0 {
			fmt.Fprintf(streams.Out, "  warm %.2fx", model.WarmSpeedup)
		}
		fmt.Fprintln(streams.Out)
	}
	return nil
}
