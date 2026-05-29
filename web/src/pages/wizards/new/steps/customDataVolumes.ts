export interface ParsedDataVolumes {
  paths: string[];
  error?: string;
}

export function parseCustomDataVolumes(input: string): ParsedDataVolumes {
  const raw = input.trim();
  if (raw === "") {
    return { paths: [], error: "Enter at least one data volume path." };
  }
  if (/[,\n\r\t ]/.test(raw)) {
    return {
      paths: [],
      error: "Enter one absolute data volume path or one numeric range.",
    };
  }

  const expanded = expandBraceRange(raw);
  if (expanded.error) {
    return { paths: [], error: expanded.error };
  }

  const seen = new Set<string>();
  const normalized: string[] = [];
  for (const path of expanded.paths) {
    if (!path.startsWith("/")) {
      return { paths: [], error: `Data volume path must be absolute: ${path}` };
    }
    const cleaned = normalizePath(path);
    if (cleaned === "/") {
      return { paths: [], error: "Data volume path cannot be /." };
    }
    if (!seen.has(cleaned)) {
      seen.add(cleaned);
      normalized.push(cleaned);
    }
  }

  return { paths: normalized };
}

function expandBraceRange(path: string): ParsedDataVolumes {
  const match = path.match(/^(.*)\{(\d+)(?:\.\.|\.\.\.)(\d+)\}(.*)$/);
  if (!match) {
    if (path.includes("{") || path.includes("}")) {
      return { paths: [], error: `Unsupported path pattern: ${path}` };
    }
    return { paths: [path] };
  }

  const [, prefix, rawStart, rawEnd, suffix] = match;
  const start = parseInt(rawStart, 10);
  const end = parseInt(rawEnd, 10);
  if (end < start) {
    return { paths: [], error: `Path range must ascend: ${path}` };
  }
  if (end - start > 255) {
    return { paths: [], error: `Path range is too large: ${path}` };
  }

  const width = rawStart.startsWith("0") || rawEnd.startsWith("0")
    ? Math.max(rawStart.length, rawEnd.length)
    : 0;
  const paths: string[] = [];
  for (let n = start; n <= end; n++) {
    const value = width > 0 ? String(n).padStart(width, "0") : String(n);
    paths.push(`${prefix}${value}${suffix}`);
  }
  return { paths };
}

function normalizePath(path: string): string {
  if (path === "/") return path;
  return path.replace(/\/+$/, "");
}
