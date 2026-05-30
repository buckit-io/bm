import { expect, test, type Page } from "@playwright/test";

const clusterName = process.env.BM_E2E_DEPLOY_CLUSTER_NAME ?? "fixture-deploy";
const hosts = (process.env.BM_E2E_DEPLOY_HOSTS ?? "deploy-node1,deploy-node2,deploy-node3,deploy-node4")
  .split(",")
  .map((host) => host.trim())
  .filter(Boolean);
const sshUser = process.env.BM_E2E_DEPLOY_SSH_USER ?? "root";
const sshPassword = process.env.BM_E2E_DEPLOY_SSH_PASSWORD ?? "buckitadmin";

test.setTimeout(10 * 60 * 1000);

function displayedVersion(tag: string) {
  return tag.replace(/^RELEASE\./, "");
}

async function next(page: Page) {
  const button = page.getByTestId("wizard-next");
  await expect(button).toBeEnabled({ timeout: 120_000 });
  await button.click();
}

async function ensurePreflightCanAdvance(page: Page) {
  const nextButton = page.getByTestId("wizard-next");
  try {
    await expect(nextButton).toBeEnabled({ timeout: 5_000 });
    return;
  } catch {
    await page.getByRole("button", { name: "Re-run" }).click();
    await expect(page.getByTestId("new-cluster-preflight-table")).toContainText(
      "Package manager available",
      { timeout: 120_000 },
    );
    await expect(nextButton).toBeEnabled({ timeout: 120_000 });
  }
}

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
  await page.reload();
  await expect(page).toHaveURL(new RegExp(`/clusters/${clusterId}$`));
  await expect(page.getByTestId("cluster-title")).toContainText(clusterName);
}

async function expectClusterVersion(page: Page, clusterId: string, version: string) {
  await expect
    .poll(
      async () => {
        const refresh = await page.request.post(
          `/api/v1/clusters/${encodeURIComponent(clusterId)}/refresh`,
        );
        expect(refresh.status()).toBe(200);
        const cluster = (await refresh.json()) as { version?: string };
        return displayedVersion(cluster.version ?? "");
      },
      { timeout: 120_000 },
    )
    .toBe(displayedVersion(version));
}

test("deploys a new Buckit cluster", async ({ page }) => {
  await page.goto("/clusters/new");

  await expect(page.getByTestId("wizard-title")).toHaveText(
    "Deploy a new Buckit cluster",
  );

  const versionOptions = page.getByTestId("new-cluster-version").locator("option");
  await expect.poll(async () => versionOptions.count(), {
    timeout: 30_000,
  }).toBeGreaterThanOrEqual(3);
  const latestVersion = await versionOptions.nth(0).getAttribute("value");
  const previousVersion = await versionOptions.nth(1).getAttribute("value");
  if (!latestVersion || !previousVersion) {
    throw new Error("Expected at least two Buckit release entries in the version dropdown");
  }

  await page.getByTestId("new-cluster-name").fill(clusterName);
  await page.getByTestId("new-cluster-version").selectOption(previousVersion);
  await page.getByTestId("new-cluster-root-user").fill("buckitadmin");
  await page.getByTestId("new-cluster-root-password").fill("buckitadmin");
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
  await ensurePreflightCanAdvance(page);
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
  const clusterId = new URL(page.url()).pathname.split("/").pop() ?? clusterName;
  await expect(page.getByTestId("cluster-title")).toContainText(clusterName);
  await expect(page.getByTestId("cluster-meta")).toContainText(displayedVersion(previousVersion));
  await expect(page.getByText(hosts[0] ?? "deploy-node1")).toBeVisible();
  await expect(page.getByText(hosts[hosts.length - 1] ?? "deploy-node4")).toBeVisible();

  await saveClusterSshConfig(page, clusterId);
  await page.getByTestId("cluster-actions-toggle").click();
  await page.getByTestId("cluster-action-cluster_upgrade_by_systemctl").click();
  await expect(page.getByTestId("cluster-operation-modal")).toBeVisible();
  await expect(page.getByTestId("cluster-operation-version")).toHaveValue(
    latestVersion,
    { timeout: 30_000 },
  );
  await page.getByTestId("cluster-operation-primary").click();
  await expect(page.getByText("RESULT")).toBeVisible({
    timeout: 300_000,
  });
  await expect(page.getByTestId("cluster-operation-modal")).toContainText(latestVersion);
  await expect(page.getByTestId("cluster-operation-modal")).toContainText("Succeeded");
  await expectClusterVersion(page, clusterId, latestVersion);
});
