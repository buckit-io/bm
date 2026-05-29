import { expect, test } from "@playwright/test";

const importURL = process.env.BM_E2E_IMPORT_URL ?? "http://minio-node1:9000";
const importUsername = process.env.BM_E2E_IMPORT_USERNAME ?? "minioadmin";
const importPassword = process.env.BM_E2E_IMPORT_PASSWORD ?? "minioadmin";
const clusterName = process.env.BM_E2E_MIGRATE_CLUSTER_NAME ?? "fixture-migrate";
const sshUser = process.env.BM_E2E_MIGRATE_SSH_USER ?? "root";
const sshPassword = process.env.BM_E2E_MIGRATE_SSH_PASSWORD ?? "minioadmin";
test.setTimeout(10 * 60 * 1000);

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
  await expect(page.getByTestId("cluster-title")).toContainText(clusterName);
  await expect(page.getByTestId("cluster-migrate-link")).toHaveCount(0);
  await expect(page.getByRole("link", { name: /Open Buckit console/ })).toBeVisible();
});
