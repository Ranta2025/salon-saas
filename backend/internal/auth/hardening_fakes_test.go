package auth_test

import (
	"salon-saas/internal/testutil"
)

// countingHasher cuenta los compares reales y los falsos (B-05: sin usuario en
// la DB, igual se gasta el trabajo de bcrypt).
type countingHasher struct {
	testutil.FakeHasher
	dummyCalls int
}

func (h *countingHasher) DummyCompare(string) bool {
	h.dummyCalls++
	return false
}
