package metric

import (
	"errors"
	"testing"
)

func TestSampleHashCanonicalizesObservationOrderAndRejectsIncompleteSamples(t *testing.T) {
	left := Sample{Ordinal: 3, Status: SampleSucceeded, Observations: []Observation{observation("damage", "2"), observation("healing", "1")}}
	right := Sample{Ordinal: 3, Status: SampleSucceeded, Observations: []Observation{observation("healing", "1"), observation("damage", "2")}}
	leftHash, err := SampleHash(left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := SampleHash(right)
	if err != nil || leftHash != rightHash {
		t.Fatalf("hashes %q %q err=%v", leftHash, rightHash, err)
	}
	left.Status = SampleCanceled
	if _, err = SampleHash(left); !errors.Is(err, ErrSampleInvalid) {
		t.Fatalf("expected canceled sample rejection, got %v", err)
	}
}
