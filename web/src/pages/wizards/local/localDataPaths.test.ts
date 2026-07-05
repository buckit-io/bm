import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { localRootOSDrivePaths, parseLocalDataPaths } from "./localDataPaths";

describe("parseLocalDataPaths", () => {
  it("expands numeric brace ranges", () => {
    assert.deepEqual(parseLocalDataPaths(["~/buckit/local/data{1...3}"]), {
      paths: [
        "~/buckit/local/data1",
        "~/buckit/local/data2",
        "~/buckit/local/data3",
      ],
    });
    assert.deepEqual(parseLocalDataPaths(["/Volumes/disk{01..02}/buckit"]), {
      paths: ["/Volumes/disk01/buckit", "/Volumes/disk02/buckit"],
    });
  });

  it("accepts explicit absolute paths and Windows paths", () => {
    assert.deepEqual(parseLocalDataPaths(["~/buckit/local/data one", "C:\\Buckit\\data2"]), {
      paths: ["~/buckit/local/data one", "C:\\Buckit\\data2"],
    });
  });

  it("ignores blank rows and marks all-blank input incomplete", () => {
    assert.deepEqual(parseLocalDataPaths(["", "  ", "/tmp/data1"]), {
      paths: ["/tmp/data1"],
    });
    assert.deepEqual(parseLocalDataPaths(["", "  "]), {
      paths: [],
      incomplete: true,
    });
  });

  it("rejects unsupported patterns and duplicates after expansion", () => {
    assert.equal(
      parseLocalDataPaths(["~/buckit/local/data{a...b}"]).error,
      "Unsupported data path pattern: ~/buckit/local/data{a...b}",
    );
    assert.equal(
      parseLocalDataPaths(["~/buckit/local/data{1...2}", "~/buckit/local/data2"]).error,
      "Duplicate data path: ~/buckit/local/data2",
    );
  });

  it("rejects relative paths and filesystem roots", () => {
    assert.equal(
      parseLocalDataPaths(["data{1...2}"]).error,
      "Data path must be absolute: data1",
    );
    assert.equal(
      parseLocalDataPaths(["/"]).error,
      "Data path cannot be a filesystem root: /",
    );
    assert.equal(
      parseLocalDataPaths(["C:\\"]).error,
      "Data path cannot be a filesystem root: C:\\",
    );
  });

  it("identifies likely root or OS drive data paths", () => {
    assert.deepEqual(
      localRootOSDrivePaths("darwin", ["/tmp/data1", "/Volumes/disk1/buckit", "~/buckit/local/data"]),
      ["/tmp/data1", "~/buckit/local/data"],
    );
    assert.deepEqual(
      localRootOSDrivePaths("windows", ["C:\\Buckit\\data1", "D:\\Buckit\\data2"]),
      ["C:\\Buckit\\data1"],
    );
    assert.deepEqual(localRootOSDrivePaths("linux", ["/tmp/data1", "/tmp/data2"]), []);
    assert.deepEqual(localRootOSDrivePaths("darwin", ["/tmp/data1"]), []);
  });
});
