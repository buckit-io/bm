import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { emptyDraft } from "../state";
import { topologyErrors } from "./topologyValidation";

describe("topologyErrors", () => {
  it("allows a single-drive standalone deployment", () => {
    const draft = emptyDraft();
    draft.hosts[0].hostname = "node1";
    draft.topology.selectedMounts = ["/var/lib/storage"];
    draft.topology.setSize = 1;
    draft.topology.parity = 0;

    assert.deepEqual(topologyErrors(draft), []);
  });

  it("still rejects non-divisible erasure-coded totals", () => {
    const draft = emptyDraft();
    draft.hosts = Array.from({ length: 17 }, (_, i) => ({
      id: `h${i + 1}`,
      hostname: `node${i + 1}`,
      port: 22,
      probe: "idle" as const,
    }));
    draft.topology.selectedMounts = ["/data1"];
    draft.topology.setSize = 2;
    draft.topology.parity = 1;

    assert.deepEqual(topologyErrors(draft), [
      "Total drives (17) is not divisible by any supported erasure set size (2–16). Add or remove drives so the total is divisible by at least one of: 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16.",
    ]);
  });
});
