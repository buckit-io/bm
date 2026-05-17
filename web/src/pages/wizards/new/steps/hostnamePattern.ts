// Detect whether a list of hostnames fits a single brace-expansion
// pattern. Used by the Add Nodes step to surface a warning when the
// operator's hosts can't be expressed as one MINIO_VOLUMES pool.
//
// A list "fits" when there exists a common prefix P and common suffix S
// such that every hostname is P + <number> + S, the numbers are a
// contiguous range, and any zero-padding is consistent.
//
// Examples:
//   ["node1", "node2", "node3"]                  → node{1...3}
//   ["node-1.foo.com", "node-2.foo.com"]         → node-{1...2}.foo.com
//   ["minio01.example", "minio02.example"]       → minio{01...02}.example (padded)
//   ["nodeA", "nodeB"]                           → no fit (non-numeric middle)
//   ["node1", "node3"]                           → no fit (gap at node2)
//   ["node1.foo.com", "legacy.foo.com"]          → no fit (no shared numeric middle)

export type PatternFit =
  | { fits: true; pattern: string; lo: number; hi: number }
  | { fits: false; reason: string };

export function detectHostnamePattern(hostnames: string[]): PatternFit {
  const list = hostnames.map((s) => s.trim()).filter(Boolean);
  if (list.length < 2) {
    // A single host trivially fits any pattern. Caller decides whether
    // to render anything for the single-host case.
    return list.length === 1
      ? { fits: true, pattern: list[0], lo: 1, hi: 1 }
      : { fits: false, reason: "No hostnames entered." };
  }

  const prefix = longestCommonPrefix(list);
  const suffix = longestCommonSuffix(list);
  // Trim prefix and suffix carefully — they could overlap on the same
  // characters when hostnames share both endpoints. Cap each on the
  // shortest hostname length.
  const middles: string[] = [];
  for (const h of list) {
    if (prefix.length + suffix.length > h.length) {
      return { fits: false, reason: "Hostnames overlap; no numeric middle." };
    }
    middles.push(h.slice(prefix.length, h.length - suffix.length));
  }

  // Every middle must be a pure decimal integer string.
  if (!middles.every((m) => /^\d+$/.test(m))) {
    return {
      fits: false,
      reason:
        "Hostnames don't share a numeric middle (e.g. node1 / node2 / node3).",
    };
  }

  // Zero-padding must be uniform if any value uses it.
  const width = middles[0].length;
  const padded = middles[0].startsWith("0") && width > 1;
  if (padded && !middles.every((m) => m.length === width)) {
    return {
      fits: false,
      reason: "Zero-padding width is inconsistent across hostnames.",
    };
  }

  const nums = middles.map((m) => parseInt(m, 10)).sort((a, b) => a - b);
  // Duplicates first — otherwise the gap check below reports them as
  // "skip from N to N" which is confusing.
  if (new Set(nums).size !== nums.length) {
    const dup = nums.find((n, i) => nums[i - 1] === n);
    return {
      fits: false,
      reason: `Duplicate hostname number (${dup} appears more than once).`,
    };
  }
  // Range must be contiguous — no gaps.
  for (let i = 1; i < nums.length; i++) {
    if (nums[i] !== nums[i - 1] + 1) {
      return {
        fits: false,
        reason: `Hostnames skip a value (got ${nums[i - 1]}, then ${nums[i]}).`,
      };
    }
  }

  const lo = nums[0];
  const hi = nums[nums.length - 1];
  const loStr = padded ? String(lo).padStart(width, "0") : String(lo);
  const hiStr = padded ? String(hi).padStart(width, "0") : String(hi);
  return {
    fits: true,
    pattern: `${prefix}{${loStr}...${hiStr}}${suffix}`,
    lo,
    hi,
  };
}

function longestCommonPrefix(xs: string[]): string {
  if (xs.length === 0) return "";
  let p = xs[0];
  for (let i = 1; i < xs.length; i++) {
    while (xs[i].indexOf(p) !== 0) {
      p = p.slice(0, p.length - 1);
      if (!p) return "";
    }
  }
  return p;
}

function longestCommonSuffix(xs: string[]): string {
  if (xs.length === 0) return "";
  const rev = (s: string) => s.split("").reverse().join("");
  return rev(longestCommonPrefix(xs.map(rev)));
}
