package training

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	evalpkg "github.com/Viking602/azem/internal/eval"
)

func TestNoiseRobustnessTrainingPreservesRawPairsAndLearnsSafeActions(t *testing.T) {
	t.Parallel()
	file, err := os.Open("../eval/testdata/noise/paired_tasks_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	pairs, err := evalpkg.ReadNoisePairs(file)
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := BuildNoiseDataset(pairs)
	if err != nil {
		t.Fatal(err)
	}
	if dataset.ID == "" || len(dataset.Examples) != len(pairs) || !bytes.Contains(dataset.Examples[0].RawNoisy, []byte("steps")) || len(dataset.Examples[0].NormalizedLabels) != len(pairs[0].NoiseLabels) {
		t.Fatalf("dataset = %+v", dataset)
	}
	first, err := TrainNoiseRobustness(dataset, 602)
	if err != nil {
		t.Fatal(err)
	}
	second, err := TrainNoiseRobustness(dataset, 602)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("training is not deterministic: %+v %+v err=%v", first, second, err)
	}
	var allLabels []evalpkg.IncidentLabelV1
	for _, example := range dataset.Examples {
		allLabels = append(allLabels, example.NormalizedLabels...)
	}
	predicted := first.Predict(allLabels)
	for _, action := range []string{ActionRequireValidator, ActionRetryRecover, ActionEscalate, ActionDeduplicate} {
		if !contains(predicted, action) {
			t.Fatalf("missing learned action %q in %v", action, predicted)
		}
	}
	evaluation, err := EvaluateNoiseRobustness(first, dataset)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.ExpectedActionRecall != 1 || evaluation.FalseSuccessRate != 0 {
		t.Fatalf("evaluation = %+v", evaluation)
	}
}

func TestNoiseRobustnessRequiresFixedSeed(t *testing.T) {
	t.Parallel()
	if _, err := TrainNoiseRobustness(NoiseDatasetV1{Version: 1, ID: "dataset", Examples: []NoiseExampleV1{{PairID: "pair"}}}, 0); err == nil {
		t.Fatal("accepted implicit training seed")
	}
}
