package engine

import (
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/simulation/random"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

func TestBuiltinTemplateInitialEventGoldenOrder(t *testing.T) {
	want := map[string][]string{
		"single-target-30s":    {"0/0/source/0"},
		"single-target-180s":   {"0/0/source/0"},
		"three-target-60s":     {"0/0/source/0", "1000/0/source/1", "2000/0/source/2"},
		"extreme-stacking-60s": {"0/0/source/0", "1/0/source/1"},
	}
	for _, template := range scenario.BuiltinTemplates() {
		t.Run(template.Definition.ID, func(t *testing.T) {
			events, err := InitialEvents(template.Definition)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, len(events))
			for index, event := range events {
				got[index] = event.String()
			}
			if !reflect.DeepEqual(got, want[template.Definition.ID]) {
				t.Fatalf("events=%v want=%v", got, want[template.Definition.ID])
			}
		})
	}
}

func TestGoldenQueueOrderSurvivesSourceAndInsertionShuffle(t *testing.T) {
	events := []Event{
		{TimeMS: 5, RulePriority: 1, SourceID: "z-source", InsertionOrdinal: 2, Kind: "damage", Version: "v1", EvaluatorID: "combat", TargetID: "target"},
		{TimeMS: 5, RulePriority: 1, SourceID: "a-source", InsertionOrdinal: 4, Kind: "damage", Version: "v1", EvaluatorID: "combat", TargetID: "target"},
		{TimeMS: 5, RulePriority: 1, SourceID: "a-source", InsertionOrdinal: 3, Kind: "damage", Version: "v1", EvaluatorID: "combat", TargetID: "target"},
	}
	want := []string{"5/1/a-source/3", "5/1/a-source/4", "5/1/z-source/2"}
	for _, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 0, 2}} {
		queue := NewQueue()
		for _, index := range order {
			if err := queue.Enqueue(events[index]); err != nil {
				t.Fatal(err)
			}
		}
		got := []string{}
		_, err := Run(queue, Limits{DurationMS: 5, MaxEvents: 3, MaxSteps: 3}, func(event Event, _ *Emitter) error {
			got = append(got, event.String())
			return nil
		})
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("order=%v got=%v err=%v", order, got, err)
		}
	}
}

func TestGoldenDurationBoundaryAndSampleStreams(t *testing.T) {
	queue := NewQueue()
	for _, event := range []Event{testEvent(10, "source", 0), testEvent(11, "source", 1)} {
		if err := queue.Enqueue(event); err != nil {
			t.Fatal(err)
		}
	}
	executed := []int64{}
	stats, err := Run(queue, Limits{DurationMS: 10, MaxEvents: 2, MaxSteps: 2}, func(event Event, _ *Emitter) error {
		executed = append(executed, event.TimeMS)
		return nil
	})
	if err != nil || !stats.ReachedSceneEnd || !reflect.DeepEqual(executed, []int64{10}) {
		t.Fatalf("stats=%#v executed=%v err=%v", stats, executed, err)
	}
	left, right := random.New(13, 7), random.New(13, 7)
	for draw := 0; draw < 16; draw++ {
		if left.Uint64() != right.Uint64() {
			t.Fatalf("draw %d diverged", draw)
		}
	}
}

func TestGoldenIndependentProcessReplay(t *testing.T) {
	if os.Getenv("SIMULATION_GOLDEN_REPLAY") == "1" {
		fmt.Print(goldenReplay())
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestGoldenIndependentProcessReplay$")
	command.Env = append(os.Environ(), "SIMULATION_GOLDEN_REPLAY=1")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSuffix(string(output), "PASS\n"), goldenReplay(); got != want {
		t.Fatalf("independent replay=%q want=%q", got, want)
	}
}

func goldenReplay() string {
	parts := []string{}
	for _, template := range scenario.BuiltinTemplates() {
		events, err := InitialEvents(template.Definition)
		if err != nil {
			panic(err)
		}
		ordered := make([]string, len(events))
		for index, event := range events {
			ordered[index] = event.String()
		}
		stream := random.New(template.Definition.DefaultSeed, 0)
		parts = append(parts, template.Definition.ID+":"+strings.Join(ordered, ",")+fmt.Sprintf(":%016x", stream.Uint64()))
	}
	return strings.Join(parts, "\n")
}
