// Host-pattern expansion.
//
// Accepts forms like:
//   node{1...4}.example.net                   → 4 hosts (three-dot form)
//   node{1..4}.example.net                    → 4 hosts (two-dot form)
//   node{01...10}.example.net                 → 10 hosts, zero-padded
//   node{1...2}.dc{1...3}.example.net         → 6 hosts (cross-product)
//   https://node{1...4}.example.net:9000/d    → 4 hosts (scheme/port/path stripped)
//
// Plain hostnames pass through unchanged:
//   localhost                                  → ["localhost"]
//   node1.example.com                          → ["node1.example.com"]
//
// Malformed patterns (unclosed braces, single-dot ranges, lo > hi) pass
// through unchanged so the operator sees their original text and can
// correct it.

// Strip URL scheme, port, and path so we operate on just the hostname
// portion. Curly braces are not legal in DNS hostnames per RFC 1035, so
// stripping is safe — anything `{...}` is always intentional expansion.
function stripUrlNoise(s: string): string {
  return s
    .trim()
    .replace(/^https?:\/\//i, "")
    .replace(/[/:].*$/, "");
}

// Expand the leftmost `{N..M}` or `{N...M}` range, then recurse on each
// result so multi-range patterns produce the full cross-product.
function expandRanges(s: string): string[] {
  const m = s.match(/\{(\d+)\.{2,3}(\d+)\}/);
  if (!m) return [s];
  const whole = m[0];
  const loStr = m[1];
  const hiStr = m[2];
  const lo = parseInt(loStr, 10);
  const hi = parseInt(hiStr, 10);
  if (isNaN(lo) || isNaN(hi) || lo > hi) return [s];
  // Detect zero-padding from the lo value: "01" → pad to 2, "001" → 3.
  const padded = loStr.startsWith("0") && loStr.length > 1;
  const width = loStr.length;
  const out: string[] = [];
  for (let i = lo; i <= hi; i++) {
    const num = padded ? String(i).padStart(width, "0") : String(i);
    out.push(...expandRanges(s.replace(whole, num)));
  }
  return out;
}

export function expandHostPattern(input: string): string[] {
  const trimmed = input.trim();
  if (!trimmed) return [];
  return expandRanges(stripUrlNoise(trimmed));
}
