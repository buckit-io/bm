export interface ParsedLocalDataPaths {
  paths: string[];
  error?: string;
  incomplete?: boolean;
}

const rangePattern = /^(.*)\{(\d+)(?:\.\.|\.\.\.)(\d+)\}(.*)$/;

export function parseLocalDataPaths(inputs: string[]): ParsedLocalDataPaths {
  const seen = new Set<string>();
  const paths: string[] = [];
  for (const input of inputs) {
    const expanded = expandLocalDataPath(input);
    if (expanded.error) return expanded;
    for (const path of expanded.paths) {
      if (!isLocalAbsolutePath(path)) {
        return { paths: [], error: `Data path must be absolute: ${path}` };
      }
      if (isLocalRootPath(path)) {
        return { paths: [], error: `Data path cannot be a filesystem root: ${path}` };
      }
      const key = normalizeLocalPathKey(path);
      if (seen.has(key)) {
        return { paths: [], error: `Duplicate data path: ${path}` };
      }
      seen.add(key);
      paths.push(trimTrailingSeparators(path));
    }
  }
  if (paths.length === 0) {
    return { paths: [], incomplete: true };
  }
  return { paths };
}

export function localRootOSDrivePaths(goos: string | undefined, paths: string[]): string[] {
  if (paths.length < 2) return [];
  if (goos === "darwin") {
    return paths.filter((path) => {
      const p = path.trim();
      return p.startsWith("~/") || (p.startsWith("/") && !p.startsWith("/Volumes/"));
    });
  }
  if (goos === "windows") {
    return paths.filter((path) => /^[Cc]:[\\/]/.test(path.trim()));
  }
  return [];
}

function expandLocalDataPath(input: string): ParsedLocalDataPaths {
  const raw = input.trim();
  if (raw === "") {
    return { paths: [] };
  }
  const match = raw.match(rangePattern);
  if (!match) {
    if (raw.includes("{") || raw.includes("}")) {
      return { paths: [], error: `Unsupported data path pattern: ${raw}` };
    }
    return { paths: [raw] };
  }
  const [, prefix, rawStart, rawEnd, suffix] = match;
  const start = parseInt(rawStart, 10);
  const end = parseInt(rawEnd, 10);
  if (end < start) {
    return { paths: [], error: `Data path range must ascend: ${raw}` };
  }
  if (end - start > 255) {
    return { paths: [], error: `Data path range is too large: ${raw}` };
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

function isLocalAbsolutePath(path: string): boolean {
  return path.startsWith("/") ||
    path.startsWith("~/") ||
    path.startsWith("~\\") ||
    /^[A-Za-z]:[\\/]/.test(path);
}

function isLocalRootPath(path: string): boolean {
  const trimmed = trimTrailingSeparators(path);
  return trimmed === "/" || trimmed === "~" || /^[A-Za-z]:$/.test(trimmed);
}

function normalizeLocalPathKey(path: string): string {
  return trimTrailingSeparators(path).toLowerCase();
}

function trimTrailingSeparators(path: string): string {
  if (path === "/") return path;
  if (/^[A-Za-z]:[\\/]$/.test(path)) return path.replace(/[\\/]$/, "");
  return path.replace(/[\\/]+$/, "");
}
