import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { DiscoveredDrive } from "../state";
import { intersectMounts, isEligibleMountUnderRoot } from "./driveIntersection";

const drive = (
  mount: string,
  patch: Partial<DiscoveredDrive> = {},
): DiscoveredDrive => ({
  device: `/dev/${mount.replace(/\W+/g, "") || "root"}`,
  mount,
  sizeBytes: 1024,
  fsType: "xfs",
  ...patch,
});

describe("driveIntersection", () => {
  it("keeps system mounts excluded when preferred root is empty or /", () => {
    const system = drive("/var", { isSystem: true });

    assert.equal(isEligibleMountUnderRoot(system, ""), false);
    assert.equal(isEligibleMountUnderRoot(system, "/"), false);
  });

  it("allows a specific preferred root under a system prefix", () => {
    const systemData = drive("/var/lib/buckit", { isSystem: true });
    const systemSibling = drive("/var/log", { isSystem: true });

    assert.equal(isEligibleMountUnderRoot(systemData, "/var/lib/buckit"), true);
    assert.equal(isEligibleMountUnderRoot(systemSibling, "/var/lib/buckit"), false);
  });

  it("intersects only mounts under the preferred root", () => {
    const result = intersectMounts(
      {
        h1: [
          drive("/data/drive1"),
          drive("/data/drive2"),
          drive("/var/lib/buckit/drive1", { isSystem: true }),
        ],
        h2: [
          drive("/data/drive1"),
          drive("/data/drive3"),
          drive("/var/lib/buckit/drive1", { isSystem: true }),
        ],
      },
      "/var/lib/buckit",
    );

    assert.deepEqual(result.common, ["/var/lib/buckit/drive1"]);
    assert.deepEqual(result.extrasByHost, {});
    assert.deepEqual(result.eligibleByHost, { h1: 1, h2: 1 });
  });

  it("keeps the default intersection on non-system data mounts", () => {
    const result = intersectMounts({
      h1: [drive("/data/drive1"), drive("/data/drive2"), drive("/var", { isSystem: true })],
      h2: [drive("/data/drive1"), drive("/data/drive3"), drive("/var", { isSystem: true })],
    });

    assert.deepEqual(result.common, ["/data/drive1"]);
    assert.deepEqual(result.extrasByHost, {
      h1: ["/data/drive2"],
      h2: ["/data/drive3"],
    });
  });
});
