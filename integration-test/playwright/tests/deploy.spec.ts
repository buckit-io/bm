import { expect, test, type Page } from "@playwright/test";

const clusterName = process.env.BM_E2E_DEPLOY_CLUSTER_NAME ?? "fixture-deploy";
const hosts = (process.env.BM_E2E_DEPLOY_HOSTS ?? "deploy-node1,deploy-node2,deploy-node3,deploy-node4")
  .split(",")
  .map((host) => host.trim())
  .filter(Boolean);
const artifactURL = process.env.BM_E2E_DEPLOY_ARTIFACT_URL;
const sshUser = process.env.BM_E2E_DEPLOY_SSH_USER ?? "root";
const sshPassword = process.env.BM_E2E_DEPLOY_SSH_PASSWORD ?? "buckitadmin";

test.setTimeout(10 * 60 * 1000);

async function next(page: Page) {
  const button = page.getByTestId("wizard-next");
  await expect(button).toBeEnabled({ timeout: 120_000 });
  await button.click();
}

test("deploys a new Buckit cluster", async ({ page }) => {
  await page.goto("/clusters/new");

  await expect(page.getByTestId("wizard-title")).toHaveText(
    "Deploy a new Buckit cluster",
  );

  if (!artifactURL) {
    throw new Error("BM_E2E_DEPLOY_ARTIFACT_URL is required");
  }

  await page.getByTestId("new-cluster-name").fill(clusterName);
  await page.getByTestId("new-cluster-version").selectOption("custom");
  await expect(page.getByTestId("new-cluster-custom-url")).toBeVisible();
  await page.getByTestId("new-cluster-custom-url").fill(artifactURL);
  await page.getByTestId("new-cluster-custom-url").blur();
  await page.getByTestId("new-cluster-root-user").fill("buckitadmin");
  await page.getByTestId("new-cluster-root-password").fill("buckitadmin");
  await expect(page.getByTestId("new-cluster-custom-url-status")).not.toContainText(
    "Paste a direct URL",
    { timeout: 30_000 },
  );
  await expect(page.getByTestId("new-cluster-custom-url-status")).not.toContainText(
    "Checking URL…",
    { timeout: 30_000 },
  );
  await expect(page.getByTestId("wizard-next")).toBeEnabled({ timeout: 30_000 });
  await next(page);

  await page.getByTestId("new-cluster-hostname-0").fill(hosts[0] ?? "deploy-node1");
  for (let i = 1; i < hosts.length; i++) {
    await page.getByTestId("new-cluster-add-row").click();
    await page.getByTestId(`new-cluster-hostname-${i}`).fill(hosts[i]);
  }

  await page.getByTestId("new-cluster-auth-password").click();
  await page.getByTestId("new-cluster-ssh-user").fill(sshUser);
  await page.getByTestId("new-cluster-ssh-password").fill(sshPassword);
  await page.getByTestId("new-cluster-probe-all").click();
  await expect(page.getByTestId("wizard-next")).toBeEnabled({ timeout: 120_000 });
  await next(page);

  await expect(page.getByTestId("new-cluster-discovery-progress")).toHaveText(
    `${hosts.length} / ${hosts.length} complete`,
    { timeout: 120_000 },
  );
  await next(page);

  await expect(page.getByText("Mode: Erasure coded")).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("Discovered drives")).toBeVisible();
  await expect(page.getByText(/Ready to deploy\./)).toBeVisible();
  await next(page);

  await expect(page.getByTestId("new-cluster-preflight-title")).toHaveText("Preflight");
  await expect(page.getByTestId("new-cluster-preflight-table")).toContainText(
    "Package manager available",
    { timeout: 120_000 },
  );
  await expect(page.getByTestId("new-cluster-preflight-table")).toContainText(
    "Selected paths not on root filesystem",
  );
  await next(page);

  await expect(page.getByText("Verify the plan before deploy.")).toBeVisible();
  await expect(page.getByText("Deployment mode")).toBeVisible();
  await expect(page.getByText("Erasure coded")).toBeVisible();
  await next(page);

  await expect(page.getByTestId("new-cluster-deploy-progress")).toHaveText(
    "100%",
    { timeout: 300_000 },
  );
  await next(page);

  await expect(page.getByTestId("new-cluster-done-title")).toContainText(
    `${clusterName} is up`,
  );
  await expect(page.getByTestId("new-cluster-console-url")).toContainText(
    "deploy-node1",
  );

  await page.getByRole("button", { name: "Go to cluster overview" }).click();
  await expect(page).toHaveURL(/\/clusters\/[^/]+$/);
  await expect(page.getByTestId("cluster-title")).toContainText(clusterName);
  await expect(page.getByText(hosts[0] ?? "deploy-node1")).toBeVisible();
  await expect(page.getByText(hosts[hosts.length - 1] ?? "deploy-node4")).toBeVisible();
});
