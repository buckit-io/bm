import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { expect, test, type Page } from "@playwright/test";

const importURL =
  process.env.BM_E2E_REPLACEMENT_IMPORT_URL ?? "http://replacement-node1:9000";
const clusterName =
  process.env.BM_E2E_REPLACEMENT_CLUSTER_NAME ?? "fixture-replacement";
const sshUser = process.env.BM_E2E_REPLACEMENT_SSH_USER ?? "root";
const sshPassword =
  process.env.BM_E2E_REPLACEMENT_SSH_PASSWORD ?? "buckitadmin";
const targetHostOverride = process.env.BM_E2E_REPLACEMENT_TARGET_HOST ?? "";

test.setTimeout(10 * 60 * 1000);

async function saveClusterSshConfig(page: Page, clusterId: string) {
  await page.getByTestId("cluster-settings-toggle").click();
  await page.getByTestId("cluster-setting-configure_ssh").click();
  await expect(
    page.getByRole("heading", { name: "SSH credentials", exact: true }),
  ).toBeVisible();
  await page.getByRole("radio", { name: "Password" }).click();
  await page.locator("#cluster-ssh-user").fill(sshUser);
  await page
    .locator(".field")
    .filter({ hasText: "SSH password" })
    .locator("input[type='password']")
    .fill(sshPassword);
  await page.getByRole("button", { name: "Test SSH connection" }).click();
  await expect(page.getByText("Reachable")).toHaveCount(4, { timeout: 120_000 });
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page).toHaveURL(new RegExp(`/clusters/${clusterId}$`));
  await expect(page.getByTestId("cluster-title")).toContainText(clusterName);
}

async function expectTargetHealthy(
  page: Page,
  clusterId: string,
  nodeId: string,
) {
  await expect
    .poll(
      async () => {
        const refresh = await page.request.post(
          `/api/v1/clusters/${encodeURIComponent(clusterId)}/refresh`,
        );
        expect(refresh.status()).toBe(200);
        const nodesResp = await page.request.get(
          `/api/v1/clusters/${encodeURIComponent(clusterId)}/nodes`,
        );
        expect(nodesResp.status()).toBe(200);
        const nodes = (await nodesResp.json()) as Array<{
          id: string;
          state?: string;
          sshable?: boolean;
          apiAccessible?: boolean;
        }>;
        const node = nodes.find((n) => n.id === nodeId);
        return node ?? null;
      },
      { timeout: 180_000 },
    )
    .toMatchObject({
      id: nodeId,
      state: "online",
      sshable: true,
      apiAccessible: true,
    });
}

async function chooseReplacementTarget(page: Page, clusterId: string) {
  const nodesResp = await page.request.get(
    `/api/v1/clusters/${encodeURIComponent(clusterId)}/nodes`,
  );
  expect(nodesResp.status()).toBe(200);
  const nodes = (await nodesResp.json()) as Array<{
    id: string;
    hostname: string;
    pool: number;
  }>;
  if (targetHostOverride) {
    const chosen = nodes.find((n) => n.hostname === targetHostOverride);
    expect(chosen).toBeTruthy();
    return chosen!;
  }
  const byPool = new Map<number, typeof nodes>();
  for (const node of nodes) {
    const bucket = byPool.get(node.pool) ?? [];
    bucket.push(node);
    byPool.set(node.pool, bucket);
  }
  const candidatePool = Array.from(byPool.values())
    .filter((group) => group.length > 1)
    .sort((a, b) => b.length - a.length)[0];
  expect(candidatePool).toBeTruthy();
  return candidatePool![0];
}

function scrubReplacementTarget(hostname: string) {
  const script = path.resolve(process.cwd(), "..", "scripts", "prepare-replacement-node.sh");
  const targetFile = path.resolve(process.cwd(), "..", ".generated", "replacement-target-host");
  fs.writeFileSync(targetFile, `${hostname}\n`, "utf8");
  execFileSync("bash", [script], {
    cwd: path.resolve(process.cwd(), "..", ".."),
    env: {
      ...process.env,
      BM_E2E_SCENARIO: "replacement",
      BM_E2E_REPLACEMENT_TARGET_SERVICE: hostname,
    },
    stdio: "inherit",
  });
}

test("provisions a replacement node on an imported Buckit cluster", async ({ page }) => {
  await page.goto("/clusters/import");

  await expect(page.getByTestId("import-page-title")).toHaveText(
    "Import existing Buckit or MinIO cluster",
  );

  await page.getByTestId("import-url").fill(importURL);
  await page.getByTestId("import-username").fill("buckitadmin");
  await page.getByTestId("import-password").fill("buckitadmin");
  await page.getByTestId("import-submit").click();

  await expect(page.getByTestId("import-modal")).toBeVisible();
  await expect(page.getByText("Cluster discovered")).toBeVisible();

  await page.getByTestId("import-cluster-name").fill(clusterName);
  await page.getByTestId("import-save").click();

  await expect(page.getByTestId("cluster-title")).toContainText(clusterName);
  await expect(page).toHaveURL(new RegExp(`/clusters/${clusterName}$`));
  const clusterId = clusterName;
  await expect(page.getByText("replacement-node1")).toBeVisible();

  await saveClusterSshConfig(page, clusterId);
  const targetNode = await chooseReplacementTarget(page, clusterId);
  scrubReplacementTarget(targetNode.hostname);
  const refresh = await page.request.post(
    `/api/v1/clusters/${encodeURIComponent(clusterId)}/refresh`,
  );
  expect(refresh.status()).toBe(200);
  await page.reload();
  await expect(page.getByText(targetNode.hostname)).toBeVisible();
  await page.getByRole("link", { name: targetNode.hostname }).click();
  await expect(page.getByRole("heading", { name: targetNode.hostname })).toBeVisible();
  const nodeId = new URL(page.url()).pathname.split("/").pop();
  expect(nodeId).toBe(targetNode.id);
  await page.getByTestId("node-actions-toggle").click();
  await page.getByTestId("node-action-node_redeploy_software").click();
  await expect(page.getByTestId("cluster-operation-modal")).toBeVisible();
  await page.getByTestId("cluster-operation-primary").click();
  await expect(page.getByText("RESULT")).toBeVisible({
    timeout: 300_000,
  });
  await expect(page.getByTestId("cluster-operation-modal")).toContainText(
    "provisioning complete",
  );
  await expect(page.getByTestId("cluster-operation-modal")).toContainText(
    "Succeeded",
  );
  await expectTargetHealthy(page, clusterId, nodeId!);
});
