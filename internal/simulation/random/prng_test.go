package random

import "testing"

func TestV1FixedVectors(t *testing.T) {
	cases := []struct {
		seed, ordinal uint64
		values        []uint64
	}{
		{seed: 0, ordinal: 0, values: []uint64{0xd22871562760c147, 0x6a89a8b758a76443, 0xf438302fb38a4ef5}},
		{seed: 1, ordinal: 0, values: []uint64{0x14c8e90e35170376, 0xeec8eb147b4daafe, 0x5e7ab017fc0613cd}},
		{seed: 1, ordinal: 7, values: []uint64{0x9b031df87481ace9, 0xbb7672a7f0eca87c, 0x6f9f29ba8da0a0ff}},
	}
	for _, testCase := range cases {
		t.Run(Version, func(t *testing.T) {
			stream := New(testCase.seed, testCase.ordinal)
			for index, want := range testCase.values {
				if got := stream.Uint64(); got != want {
					t.Errorf("seed=%d ordinal=%d value[%d]=%#x want=%#x", testCase.seed, testCase.ordinal, index, got, want)
				}
			}
		})
	}
}

func TestStreamsAreIndependentAndUniformIsExact(t *testing.T) {
	left, right := New(42, 0), New(42, 0)
	for index := 0; index < 20; index++ {
		if left.Uint64() != right.Uint64() {
			t.Fatal("same seed/ordinal diverged")
		}
	}
	first, second := New(42, 0), New(42, 1)
	if first.Uint64() == second.Uint64() {
		t.Fatal("sample ordinal did not derive a distinct stream")
	}
	stream := New(1, 0)
	ratio := stream.Uniform()
	if !ratio.Valid() {
		t.Fatalf("ratio=%#v", ratio)
	}
	for _, bound := range []uint64{1, 3, 10, 1<<63 + 1} {
		if value, err := stream.Uint64n(bound); err != nil || value >= bound {
			t.Fatalf("bound=%d value=%d err=%v", bound, value, err)
		}
	}
	if _, err := stream.Uint64n(0); err == nil {
		t.Fatal("expected zero-bound error")
	}
}
