import { expect, test } from "@playwright/test";

const importURL = process.env.BM_E2E_IMPORT_URL ?? "http://buckit-node1:9000";

test("imports an existing Buckit cluster", async ({ page }) => {
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

  await page.getByTestId("import-cluster-name").fill("fixture-import");
  await page.getByTestId("import-save").click();

  await expect(page).toHaveURL(/\/clusters\/[^/]+$/);
  await expect(page.getByTestId("cluster-title")).toContainText("fixture-import");
  await expect(page.getByTestId("cluster-meta")).toContainText("4 nodes");
  await expect(page.getByText("buckit-node1")).toBeVisible();
  await expect(page.getByText("buckit-node4")).toBeVisible();
});
