package routeeval

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Viking602/azem/internal/session"
)

const minimumHeldOutTasksV1 = 2

type BaselineComparisonV1 struct {
	Name                string  `json:"name"`
	HeldOutTasks        int     `json:"held_out_tasks"`
	SuccessRate         float64 `json:"success_rate"`
	MeanPredicted       float64 `json:"mean_predicted"`
	BrierScore          float64 `json:"brier_score"`
	FalsePassRate       float64 `json:"false_pass_rate"`
	MeanSelectionRegret float64 `json:"mean_selection_regret"`
}

type routeScorer interface {
	Name() string
	Score(session.RouteOutcomeV1) float64
}

func CompareTransparentBaselines(dataset DatasetV1) ([]BaselineComparisonV1, error) {
	if err := dataset.Validate(); err != nil {
		return nil, err
	}
	var training []session.RouteOutcomeV1
	heldOut := make(map[string][]session.RouteOutcomeV1)
	for _, outcome := range dataset.Outcomes {
		switch outcome.Split {
		case "train":
			if outcome.Correctness == "pass" || outcome.Correctness == "fail" {
				training = append(training, outcome)
			}
		case "held_out":
			heldOut[outcome.TaskID] = append(heldOut[outcome.TaskID], outcome)
		}
	}
	if len(training) == 0 || len(heldOut) < minimumHeldOutTasksV1 {
		return nil, fmt.Errorf("routeeval: comparison requires training and at least %d held-out tasks", minimumHeldOutTasksV1)
	}
	sort.Slice(training, func(i, j int) bool { return training[i].ID < training[j].ID })
	scorers := []routeScorer{newRuleScorer(training), newLogisticScorer(training), newBanditScorer(training)}
	result := make([]BaselineComparisonV1, 0, len(scorers))
	for _, scorer := range scorers {
		comparison := BaselineComparisonV1{Name: scorer.Name()}
		taskIDs := make([]string, 0, len(heldOut))
		for taskID := range heldOut {
			taskIDs = append(taskIDs, taskID)
		}
		sort.Strings(taskIDs)
		for _, taskID := range taskIDs {
			alternatives := append([]session.RouteOutcomeV1(nil), heldOut[taskID]...)
			if len(alternatives) < 2 {
				return nil, fmt.Errorf("routeeval: held-out task %q lacks paired routes", taskID)
			}
			sort.Slice(alternatives, func(i, j int) bool { return routeIdentity(alternatives[i]) < routeIdentity(alternatives[j]) })
			chosen := alternatives[0]
			predicted := scorer.Score(chosen)
			oracle := actualSuccess(chosen)
			for _, alternative := range alternatives[1:] {
				score := scorer.Score(alternative)
				if score > predicted {
					chosen, predicted = alternative, score
				}
				oracle = max(oracle, actualSuccess(alternative))
			}
			actual := actualSuccess(chosen)
			comparison.HeldOutTasks++
			comparison.SuccessRate += actual
			comparison.MeanPredicted += predicted
			delta := predicted - actual
			comparison.BrierScore += delta * delta
			if predicted >= 0.5 && actual == 0 {
				comparison.FalsePassRate++
			}
			comparison.MeanSelectionRegret += oracle - actual
		}
		count := float64(comparison.HeldOutTasks)
		comparison.SuccessRate /= count
		comparison.MeanPredicted /= count
		comparison.BrierScore /= count
		comparison.FalsePassRate /= count
		comparison.MeanSelectionRegret /= count
		result = append(result, comparison)
	}
	return result, nil
}

type routeStats struct {
	Count     int
	Successes int
}

type ruleScorer struct {
	byContextRoute map[string]routeStats
	byRoute        map[string]routeStats
}

func newRuleScorer(training []session.RouteOutcomeV1) ruleScorer {
	scorer := ruleScorer{byContextRoute: make(map[string]routeStats), byRoute: make(map[string]routeStats)}
	for _, outcome := range training {
		actual := int(actualSuccess(outcome))
		contextKey := contextIdentity(outcome) + "\x00" + routeIdentity(outcome)
		stats := scorer.byContextRoute[contextKey]
		stats.Count++
		stats.Successes += actual
		scorer.byContextRoute[contextKey] = stats
		routeKey := routeIdentity(outcome)
		stats = scorer.byRoute[routeKey]
		stats.Count++
		stats.Successes += actual
		scorer.byRoute[routeKey] = stats
	}
	return scorer
}

func (ruleScorer) Name() string { return "empirical_rules" }

func (scorer ruleScorer) Score(outcome session.RouteOutcomeV1) float64 {
	stats, exists := scorer.byContextRoute[contextIdentity(outcome)+"\x00"+routeIdentity(outcome)]
	if !exists {
		stats = scorer.byRoute[routeIdentity(outcome)]
	}
	return float64(stats.Successes+1) / float64(stats.Count+2)
}

type logisticScorer struct {
	weights map[string]float64
}

func newLogisticScorer(training []session.RouteOutcomeV1) logisticScorer {
	scorer := logisticScorer{weights: make(map[string]float64)}
	for epoch := range 200 {
		rate := 0.15 / math.Sqrt(float64(epoch+1))
		for _, outcome := range training {
			features := routeFeatures(outcome)
			prediction := sigmoid(sumWeights(scorer.weights, features))
			errorValue := actualSuccess(outcome) - prediction
			for _, feature := range features {
				scorer.weights[feature] += rate * (errorValue - 0.001*scorer.weights[feature])
			}
		}
	}
	return scorer
}

func (logisticScorer) Name() string { return "logistic_regression" }
func (scorer logisticScorer) Score(outcome session.RouteOutcomeV1) float64 {
	return sigmoid(sumWeights(scorer.weights, routeFeatures(outcome)))
}

type banditScorer struct {
	byContextRoute map[string]routeStats
	contextCount   map[string]int
}

func newBanditScorer(training []session.RouteOutcomeV1) banditScorer {
	scorer := banditScorer{byContextRoute: make(map[string]routeStats), contextCount: make(map[string]int)}
	for _, outcome := range training {
		contextKey := contextIdentity(outcome)
		key := contextKey + "\x00" + routeIdentity(outcome)
		stats := scorer.byContextRoute[key]
		stats.Count++
		stats.Successes += int(actualSuccess(outcome))
		scorer.byContextRoute[key] = stats
		scorer.contextCount[contextKey]++
	}
	return scorer
}

func (banditScorer) Name() string { return "beta_ucb" }
func (scorer banditScorer) Score(outcome session.RouteOutcomeV1) float64 {
	contextKey := contextIdentity(outcome)
	stats := scorer.byContextRoute[contextKey+"\x00"+routeIdentity(outcome)]
	posterior := float64(stats.Successes+1) / float64(stats.Count+2)
	exploration := 0.2 * math.Sqrt(math.Log(float64(scorer.contextCount[contextKey]+2))/float64(stats.Count+1))
	return min(posterior+exploration, 1)
}

func routeFeatures(outcome session.RouteOutcomeV1) []string {
	route := routeIdentity(outcome)
	return []string{
		"bias", "task=" + outcome.TaskClass, "repo=" + outcome.Repository, "toolchain=" + outcome.Toolchain,
		"route=" + route, "budget=" + outcome.Budget, "policy=" + outcome.PolicyVersion, "task_route=" + outcome.TaskClass + "|" + route,
	}
}

func sumWeights(weights map[string]float64, features []string) float64 {
	var sum float64
	for _, feature := range features {
		sum += weights[feature]
	}
	return sum
}

func sigmoid(value float64) float64 {
	if value >= 0 {
		exponent := math.Exp(-value)
		return 1 / (1 + exponent)
	}
	exponent := math.Exp(value)
	return exponent / (1 + exponent)
}

func actualSuccess(outcome session.RouteOutcomeV1) float64 {
	if outcome.Correctness == "pass" {
		return 1
	}
	return 0
}

func contextIdentity(outcome session.RouteOutcomeV1) string {
	return strings.Join([]string{outcome.TaskClass, outcome.Repository, outcome.Toolchain, outcome.PolicyVersion}, "\x00")
}

func routeIdentity(outcome session.RouteOutcomeV1) string {
	return strings.Join([]string{outcome.Provider, outcome.Model, outcome.Budget}, "\x00")
}
