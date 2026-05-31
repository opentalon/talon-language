package optimize

import "math/rand"

// rngWithSeed is a tiny test helper so tests can construct deterministic
// RNGs without importing rand in every file. Kept separate from the GA's
// internal seeding to make test fixtures easy to spot.
func rngWithSeed(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}
