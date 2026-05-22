package deploy

import (
	"time"

	"github.com/buckit-io/bm/internal/domain"
)

// RestoreVersionsCacheForTest seeds the in-process version cache and returns a
// restore function. It exists so cross-package integration tests can pin the
// artifact catalog without depending on live GitHub release state.
func RestoreVersionsCacheForTest(versions []domain.BuckitVersion) func() {
	cacheMu.Lock()
	oldAt := cachedAt
	oldResult := cachedResult
	cachedAt = time.Now()
	cachedResult = append([]domain.BuckitVersion(nil), versions...)
	cacheMu.Unlock()
	return func() {
		cacheMu.Lock()
		cachedAt = oldAt
		cachedResult = oldResult
		cacheMu.Unlock()
	}
}
