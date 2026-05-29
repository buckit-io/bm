export type DeploymentMode = "empty" | "standalone" | "erasure";

export function deriveDeploymentMode(totalDrives: number): DeploymentMode {
  if (totalDrives <= 0) return "empty";
  if (totalDrives === 1) return "standalone";
  return "erasure";
}
