const WINDOWS_PATH_RE = /^[a-zA-Z]:(?:[\\/]|$)|^\\\\/;
const WINDOWS_DRIVE_ROOT_RE = /^[a-zA-Z]:\/$/;
const WINDOWS_DRIVE_ONLY_RE = /^[a-zA-Z]:$/;

/**
 * Produces a canonical comparison key for a filesystem path.
 * - Trims surrounding whitespace
 * - Normalizes backslashes to forward slashes and collapses repeated separators
 *   (preserving a leading `//` for UNC paths)
 * - Strips trailing slashes except for root (`/`) or a Windows drive root (`c:/`)
 * - Lowercases on Windows (when `os === 'windows'` or the path looks like a Windows drive/UNC path)
 */
export function normalizePathKey(p: string, os?: string): string {
  const trimmed = p.trim();
  if (!trimmed) return '';

  const isWin = os?.toLowerCase() === 'windows' || WINDOWS_PATH_RE.test(trimmed);
  const isUnc = /^\\\\/.test(trimmed) || (isWin && /^\/\//.test(trimmed));

  let normalized = trimmed.replace(/\\/g, '/');
  if (isUnc) {
    const rest = normalized.replace(/^\/+/, '').replace(/\/+/g, '/').replace(/\/+$/, '');
    normalized = rest ? `//${rest}` : '//';
  } else {
    normalized = normalized.replace(/\/+/g, '/');
    if (normalized !== '/' && !WINDOWS_DRIVE_ROOT_RE.test(normalized)) {
      normalized = normalized.replace(/\/+$/, '');
    }
  }

  return isWin ? normalized.toLowerCase() : normalized;
}

/**
 * Compares two filesystem paths for equality using platform-appropriate normalization.
 * Returns `false` if either path is empty/whitespace.
 */
export function pathsEqual(a: string, b: string, os?: string): boolean {
  const ka = normalizePathKey(a, os);
  const kb = normalizePathKey(b, os);
  return ka !== '' && ka === kb;
}

/**
 * Returns the final path segment (basename) for either POSIX or Windows paths.
 * Returns `''` for empty strings, root `/`, or bare Windows drive roots like `C:\`.
 */
export function pathBasename(p: string): string {
  const clean = p.trim().replace(/[\\/]+$/, '');
  if (!clean) return '';
  const parts = clean.split(/[\\/]/).filter(Boolean);
  if (parts.length === 0) return '';
  if (parts.length === 1 && WINDOWS_DRIVE_ONLY_RE.test(parts[0])) {
    return '';
  }
  return parts[parts.length - 1] ?? '';
}

/**
 * Returns the parent directory path for either POSIX or Windows paths.
 * Returns `''` for empty strings, root `/`, or bare Windows drive roots like `C:\`.
 */
export function pathDirname(p: string): string {
  const clean = p.trim().replace(/[\\/]+$/, '');
  if (!clean || WINDOWS_DRIVE_ONLY_RE.test(clean)) return '';
  const idx = Math.max(clean.lastIndexOf('/'), clean.lastIndexOf('\\'));
  if (idx < 0) return '';
  if (idx === 0) return clean[0];
  const parent = clean.slice(0, idx);
  if (WINDOWS_DRIVE_ONLY_RE.test(parent)) {
    return `${parent}${clean[idx]}`;
  }
  return parent;
}

/**
 * Returns the parent directory name (second-to-last segment) for disambiguating
 * colliding library names, or `''` if none exists.
 */
export function pathParent(p: string): string {
  const clean = p.trim().replace(/[\\/]+$/, '');
  if (!clean) return '';
  const parts = clean.split(/[\\/]/).filter(Boolean);
  return parts.length >= 2 ? parts[parts.length - 2] : '';
}

/**
 * Suggests a human-readable library display name from a path, disambiguating
 * against `existingNames` (case-insensitively) using the parent folder name or a counter.
 */
export function suggestLibraryName(path: string, existingNames: string[]): string {
  const base = pathBasename(path) || 'Library';
  const taken = new Set(existingNames.map((n) => n.trim().toLowerCase()).filter(Boolean));
  if (!taken.has(base.toLowerCase())) {
    return base;
  }

  const parent = pathParent(path);
  if (parent) {
    const withParent = `${base} (${parent})`;
    if (!taken.has(withParent.toLowerCase())) {
      return withParent;
    }
    for (let i = 2; ; i++) {
      const candidate = `${withParent} ${i}`;
      if (!taken.has(candidate.toLowerCase())) {
        return candidate;
      }
    }
  }

  for (let i = 2; ; i++) {
    const candidate = `${base} (${i})`;
    if (!taken.has(candidate.toLowerCase())) {
      return candidate;
    }
  }
}

/**
 * Returns the OS-appropriate label for revealing a file in the native file manager.
 */
export function revealActionLabel(os?: string): string {
  switch (os?.toLowerCase()) {
    case 'windows':
      return 'Reveal in Explorer';
    case 'darwin':
      return 'Reveal in Finder';
    default:
      return 'Reveal in File Manager';
  }
}
