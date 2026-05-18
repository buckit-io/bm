import { Navigate, Route, Routes } from "react-router-dom";
import { AppShell } from "./layouts/AppShell";
import { Placeholder } from "./pages/Placeholder";
import { Login } from "./pages/Login";
import { Welcome } from "./pages/Welcome";
import { Clusters } from "./pages/Clusters";
import { Import } from "./pages/Import";
import { Settings } from "./pages/Settings";
import { History } from "./pages/History";
import { ClusterDetailLayout } from "./pages/cluster/ClusterDetail";
import { NodeDetail } from "./pages/cluster/NodeDetail";
import { NodeLogs } from "./pages/cluster/NodeLogs";
import { NodeTrace } from "./pages/cluster/NodeTrace";
import { ClusterSettings } from "./pages/cluster/ClusterSettings";
import { ClusterSshSettings } from "./pages/cluster/ClusterSshSettings";
import { NewClusterWizard } from "./pages/wizards/new/Wizard";
import { MigrationWizard } from "./pages/wizards/migrate/Wizard";

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/welcome" element={<Welcome />} />

      {/* Wizards render outside the global shell (own chrome) */}
      <Route path="/clusters/new" element={<NewClusterWizard />} />
      <Route path="/clusters/migrate" element={<MigrationWizard />} />

      {/* Everything else gets the global shell */}
      <Route element={<AppShell />}>
        <Route index element={<Navigate to="/clusters" replace />} />
        <Route path="/clusters" element={<Clusters />} />
        <Route path="/clusters/import" element={<Import />} />

        <Route path="/clusters/:clusterId" element={<ClusterDetailLayout />} />
        <Route path="/clusters/:clusterId/nodes/:nodeId" element={<NodeDetail />} />
        <Route path="/clusters/:clusterId/nodes/:nodeId/logs" element={<NodeLogs />} />
        <Route path="/clusters/:clusterId/nodes/:nodeId/trace" element={<NodeTrace />} />
        <Route path="/clusters/:clusterId/settings" element={<ClusterSettings />} />
        <Route path="/clusters/:clusterId/ssh" element={<ClusterSshSettings />} />

        <Route path="/history" element={<History />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="*" element={<Placeholder title="Not found" />} />
      </Route>
    </Routes>
  );
}
