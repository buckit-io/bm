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
