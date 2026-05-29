import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { parseCustomDataVolumes } from "./customDataVolumes";

describe("parseCustomDataVolumes", () => {
  it("accepts a single absolute path", () => {
    assert.deepEqual(parseCustomDataVolumes("/tmp/buckit-data"), {
      paths: ["/tmp/buckit-data"],
    });
  });

  it("expands numeric brace ranges", () => {
    assert.deepEqual(parseCustomDataVolumes("/data/drive{1..4}"), {
      paths: ["/data/drive1", "/data/drive2", "/data/drive3", "/data/drive4"],
    });
    assert.deepEqual(parseCustomDataVolumes("/data/drive{1...4}"), {
      paths: ["/data/drive1", "/data/drive2", "/data/drive3", "/data/drive4"],
    });
  });

  it("expands padded brace ranges", () => {
    assert.deepEqual(parseCustomDataVolumes("/data/drive{01..03}"), {
      paths: ["/data/drive01", "/data/drive02", "/data/drive03"],
    });
  });

  it("rejects relative paths and root", () => {
    assert.equal(parseCustomDataVolumes("data/drive1").error, "Data volume path must be absolute: data/drive1");
    assert.equal(parseCustomDataVolumes("/").error, "Data volume path cannot be /.");
  });

  it("rejects comma and whitespace separated paths", () => {
    assert.equal(
      parseCustomDataVolumes("/data/a,\n/data/b").error,
      "Enter one absolute data volume path or one numeric range.",
    );
    assert.equal(
      parseCustomDataVolumes("/data/a /data/b").error,
      "Enter one absolute data volume path or one numeric range.",
    );
  });
});
