package operations

import (
	"context"
	"errors"

	"github.com/buckit-io/bm/internal/tasks"
)

// RegisterAll wires every M7 executor into the tasks registry. Call once at
// startup with the constructed Deps.
func RegisterAll(deps Deps) {
	// Signal
	tasks.OverwriteRegister(tasks.OpFreezeAPI, &freezeExecutor{deps: deps})
	tasks.OverwriteRegister(tasks.OpUnfreezeAPI, &unfreezeExecutor{deps: deps})
	tasks.OverwriteRegister(tasks.OpStopCluster, &stopClusterExecutor{deps: deps})

	// Admin w/ progression
	tasks.OverwriteRegister(tasks.OpRestartCluster, &restartClusterExecutor{deps: deps})
	tasks.OverwriteRegister(tasks.OpStartHeal, &startHealExecutor{deps: deps})
	tasks.OverwriteRegister(tasks.OpClusterUpgradeByAdminUpdate, &clusterUpgradeByAdminUpdateExecutor{deps: deps})

	// Orchestrated (cluster-wide)
	tasks.OverwriteRegister(tasks.OpStartCluster, &startClusterExecutor{deps: deps})
	tasks.OverwriteRegister(tasks.OpRollingRestart, &rollingRestartExecutor{deps: deps})
	tasks.OverwriteRegister(tasks.OpClusterUpgradeBySystemctl, &clusterUpgradeBySystemctlExecutor{deps: deps})
	tasks.OverwriteRegister(tasks.OpRotateRootCreds, &rotateRootCredsExecutor{deps: deps})
	tasks.OverwriteRegister(tasks.OpRedeploySoftware, &redeployExecutor{deps: deps})

	// Host-scoped service ops
	tasks.OverwriteRegister(tasks.OpSystemctlRestart, &hostSystemctlExecutor{deps: deps, verb: "restart", waitHealthy: true})
	tasks.OverwriteRegister(tasks.OpSystemctlStop, &hostSystemctlExecutor{deps: deps, verb: "stop", waitHealthy: false})
	tasks.OverwriteRegister(tasks.OpSystemctlStart, &hostSystemctlExecutor{deps: deps, verb: "start", waitHealthy: true})

	// Host-scoped reboot ops
	tasks.OverwriteRegister(tasks.OpRebootHost, &rebootHostExecutor{deps: deps})
	tasks.OverwriteRegister(tasks.OpShutdownHost, &shutdownHostExecutor{deps: deps})

	// M7.5 stubs — registered so dispatch returns a clean 400, not 404.
	tasks.OverwriteRegister(tasks.OpAddPool, &notImplementedExecutor{kind: "add_pool"})
}

// notImplementedExecutor is the M7.5 stub. Dispatch is rejected at Validate
// so the operator gets an immediate 400; the executor body never runs.
type notImplementedExecutor struct{ kind string }

func (e *notImplementedExecutor) Validate(req tasks.DispatchRequest) error {
	return errors.New(e.kind + " is not yet implemented (M7.5)")
}

func (e *notImplementedExecutor) Execute(ctx context.Context, run *tasks.Run) error {
	return errors.New(e.kind + " is not yet implemented (M7.5)")
}
