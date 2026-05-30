import { expect, test, type Page } from "@playwright/test";

const importURL = process.env.BM_E2E_IMPORT_URL ?? "http://minio-node1:9000";
const importUsername = process.env.BM_E2E_IMPORT_USERNAME ?? "minioadmin";
const importPassword = process.env.BM_E2E_IMPORT_PASSWORD ?? "minioadmin";
const clusterName = process.env.BM_E2E_MIGRATE_CLUSTER_NAME ?? "fixture-migrate";
const sshUser = process.env.BM_E2E_MIGRATE_SSH_USER ?? "root";
const sshPassword = process.env.BM_E2E_MIGRATE_SSH_PASSWORD ?? "minioadmin";
const rotatedRootPassword =
  process.env.BM_E2E_MIGRATE_ROTATED_ROOT_PASSWORD ?? "newpassword123";
test.setTimeout(10 * 60 * 1000);

async function saveClusterSshConfig(page: Page, clusterId: string) {
  const resp = await page.request.put(`/api/v1/clusters/${encodeURIComponent(clusterId)}/ssh`, {
    data: {
      ssh: {
        authMethod: "password",
        user: sshUser,
        port: 22,
        password: sshPassword,
        sudo: false,
      },
      overrides: {},
    },
  });
  expect(resp.status()).toBe(204);
}

async function expectClusterRefreshHealthy(page: Page, clusterId: string) {
  await expect
    .poll(
      async () => {
        const refresh = await page.request.post(
          `/api/v1/clusters/${encodeURIComponent(clusterId)}/refresh`,
        );
        expect(refresh.status()).toBe(200);
        return (await refresh.json()) as { engine?: string; health?: string };
      },
      { timeout: 120_000 },
    )
    .toMatchObject({ engine: "buckit", health: "healthy" });
}

test("migrates a MinIO cluster to Buckit", async ({ page }) => {
  await page.goto("/clusters/import");

  await expect(page.getByTestId("import-page-title")).toHaveText(
    "Import existing Buckit or MinIO cluster",
  );

  await page.getByTestId("import-url").fill(importURL);
  await page.getByTestId("import-username").fill(importUsername);
  await page.getByTestId("import-password").fill(importPassword);
  await page.getByTestId("import-submit").click();

  await expect(page.getByTestId("import-modal")).toBeVisible();
  await expect(page.getByText("Cluster discovered")).toBeVisible();
  await page.getByTestId("import-cluster-name").fill(clusterName);
  await page.getByTestId("import-save").click();

  await expect(page).toHaveURL(/\/clusters\/[^/]+$/);
  await expect(page.getByTestId("cluster-title")).toContainText(clusterName);
  await expect(page.getByTestId("cluster-migrate-link")).toBeVisible();
  await page.getByTestId("cluster-migrate-link").click();

  await expect(page.getByTestId("wizard-title")).toContainText(`Migrate ${clusterName} to Buckit`);
  await expect(page.getByTestId("migrate-version")).toBeEnabled({ timeout: 30_000 });
  await page.getByTestId("wizard-next").click();

  await page.getByTestId("migrate-auth-password").click();
  await page.getByTestId("migrate-ssh-user").fill(sshUser);
  await page.getByTestId("migrate-ssh-password").fill(sshPassword);
  await page.getByTestId("migrate-probe-all").click();
  await expect(page.getByTestId("wizard-next")).toBeEnabled({ timeout: 120_000 });
  await page.getByTestId("wizard-next").click();

  await expect(page.getByTestId("migrate-snapshot-card")).toContainText("Buckets", {
    timeout: 120_000,
  });
  await expect(page.getByTestId("migrate-snapshot-card")).toContainText("1", {
    timeout: 120_000,
  });
  await expect(page.getByTestId("migrate-preflight-table")).toContainText(
    "MinIO service detected on every host",
    { timeout: 120_000 },
  );
  await expect(page.getByTestId("migrate-preflight-table")).toContainText(
    "Buckit RPM reachable from each host",
    { timeout: 120_000 },
  );
  await expect(page.getByTestId("wizard-next")).toBeEnabled({ timeout: 120_000 });
  await page.getByTestId("wizard-next").click();

  await expect(page.getByTestId("migrate-status")).toContainText("Cutover", { timeout: 300_000 });
  await expect(page.getByTestId("migrate-verify-table")).toContainText("Buckets", {
    timeout: 300_000,
  });
  await expect(page.getByText("Migration complete. Rollback to MinIO remains available")).toBeVisible({
    timeout: 300_000,
  });
  await page.getByTestId("migrate-finish").click();

  await expect(page).toHaveURL(/\/clusters\/[^/]+$/);
  const clusterId = new URL(page.url()).pathname.split("/").pop() ?? clusterName;
  await expect(page.getByTestId("cluster-title")).toContainText(clusterName);
  await expect(page.getByTestId("cluster-migrate-link")).toHaveCount(0);
  await expect(page.getByRole("link", { name: /Open Buckit console/ })).toBeVisible();

  await saveClusterSshConfig(page, clusterId);
  await page.reload();
  await expect(page).toHaveURL(new RegExp(`/clusters/${clusterId}$`));
  await page.getByTestId("cluster-actions-toggle").click();
  await page.getByTestId("cluster-action-rotate_root_creds").click();
  await expect(page.getByTestId("cluster-operation-modal")).toBeVisible();
  await page.getByTestId("rotate-root-password-input").fill(rotatedRootPassword);
  await page.getByTestId("rotate-root-password-confirm-name").fill(clusterName);
  await page.getByTestId("cluster-operation-primary").click();
  await expect(page.getByText("RESULT")).toBeVisible({
    timeout: 300_000,
  });
  await expect(page.getByTestId("cluster-operation-modal")).toContainText("Succeeded");
  await expect(page.getByTestId("cluster-operation-modal")).toContainText("Root user");
  await expect(page.getByTestId("cluster-operation-modal")).toContainText(
    "cluster healthy with rotated credentials",
  );
  await expectClusterRefreshHealthy(page, clusterId);
});
