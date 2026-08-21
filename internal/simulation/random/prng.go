// Package random implements the engine-owned, versioned simulation PRNG.
package random

import "fmt"

const Version = "simulation-prng-v1"

const (
	seedDomain   uint64 = 0x73696d756c617469 // "simulati"
	sampleDomain uint64 = 0x6f6e2d73616d706c // "on-sampl"
	gamma        uint64 = 0x9e3779b97f4a7c15
)

// Stream has no global state and is constructed solely from a normalized seed
// and stable sample ordinal. Its algorithm is splitmix64 with frozen constants.
type Stream struct{ state uint64 }

func New(seed, sampleOrdinal uint64) Stream {
	state := mix(seed ^ seedDomain)
	state = mix(state ^ sampleOrdinal ^ sampleDomain)
	return Stream{state: state}
}

func (s *Stream) Uint64() uint64 {
	s.state += gamma
	return mix(s.state)
}

// Uint64n uses rejection sampling so the result is not biased when bound does
// not divide the fixed uint64 domain.
func (s *Stream) Uint64n(bound uint64) (uint64, error) {
	if bound == 0 {
		return 0, fmt.Errorf("random bound must be positive")
	}
	threshold := -bound % bound
	for {
		value := s.Uint64()
		if value >= threshold {
			return value % bound, nil
		}
	}
}

// Ratio is the canonical exact [0,1) mapping: a 53-bit integer numerator over
// 2^53. It avoids introducing a float value into engine or Metric contracts.
type Ratio struct{ Numerator, Denominator uint64 }

func (r Ratio) Valid() bool      { return r.Denominator == 1<<53 && r.Numerator < r.Denominator }
func (s *Stream) Uniform() Ratio { return Ratio{Numerator: s.Uint64() >> 11, Denominator: 1 << 53} }

func mix(value uint64) uint64 {
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}
