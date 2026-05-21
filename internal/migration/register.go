package migration

import (
	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/tasks"
)

// Deps groups the repos and pools the cutover + rollback executors need.
// Mirrors operations.Deps so cmd/bm/web.go's wiring stays uniform.
type Deps struct {
	Clusters     *clusters.Repo
	ClusterAdmin *clusteradmin.Repo
	AdminPool    *admin.Pool
	SSHPool      *bmssh.Pool
}

// Register wires the migrate executors into the tasks registry. Idempotent —
// calling twice overwrites, matching the deploy.Register / RegisterAll pattern.
func Register(deps Deps) {
	installer := NewInstaller(deps.SSHPool)
	tasks.OverwriteRegister(tasks.OpMigrateCutover, &CutoverExecutor{
		Installer:    installer,
		Clusters:     deps.Clusters,
		ClusterAdmin: deps.ClusterAdmin,
		AdminPool:    deps.AdminPool,
	})
	tasks.OverwriteRegister(tasks.OpMigrateRollback, &RollbackExecutor{
		Installer:    installer,
		Clusters:     deps.Clusters,
		ClusterAdmin: deps.ClusterAdmin,
		AdminPool:    deps.AdminPool,
		SSHPool:      deps.SSHPool,
	})
}
