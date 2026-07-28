package operations

import "time"

// SetRotateRootCredsPostRestartWaitForTest overrides the post-restart health
// wait for cross-package integration tests and returns a restore function.
func SetRotateRootCredsPostRestartWaitForTest(opts WaitOptions) func() {
	old := rotateRootCredsPostRestartWait
	rotateRootCredsPostRestartWait = opts
	return func() {
		rotateRootCredsPostRestartWait = old
	}
}

// SetRotateRootCredsRestartRequestTimeoutForTest overrides the admin restart
// request timeout for cross-package integration tests and returns a restore function.
func SetRotateRootCredsRestartRequestTimeoutForTest(timeout time.Duration) func() {
	old := rotateRootCredsRestartRequestTimeout
	rotateRootCredsRestartRequestTimeout = timeout
	return func() {
		rotateRootCredsRestartRequestTimeout = old
	}
}

// SetClusterUpgradePostRestartWaitForTest overrides the shared post-restart
// health and version-convergence wait for cross-package integration tests and
// returns a restore function.
func SetClusterUpgradePostRestartWaitForTest(opts WaitOptions) func() {
	old := clusterUpgradePostRestartWait
	clusterUpgradePostRestartWait = opts
	return func() {
		clusterUpgradePostRestartWait = old
	}
}
