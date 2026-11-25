import type { Preset } from './types';

const UPPERCASE_TOKENS: Record<string, string> = {
  tpu: 'TPU',
  pla: 'PLA',
  petg: 'PETG',
  abs: 'ABS',
  asa: 'ASA',
  pc: 'PC',
  pa: 'PA',
  usb: 'USB',
  usbc: 'USB-C',
  hdmi: 'HDMI',
  led: 'LED',
  rgb: 'RGB',
  din: 'DIN',
  iso: 'ISO',
  ansi: 'ANSI',
  cnc: 'CNC',
  pcb: 'PCB',
  rpi: 'RPi',
  ssd: 'SSD',
  hdd: 'HDD',
  nvme: 'NVMe',
  psu: 'PSU',
  sfx: 'SFX',
  atx: 'ATX',
  vesa: 'VESA',
  id: 'ID',
  od: 'OD',
  stl: 'STL',
  step: 'STEP',
  cad: 'CAD',
};

const DIMENSION_RE = /^\d+(?:\.\d+)?(?:x\d+(?:\.\d+)?)*(?:mm|cm|in|deg|mil)$/i;
const RACK_UNIT_RE = /^(\d+(?:\.\d+)?)u$/i;
const METRIC_FASTENER_RE = /^m(\d+(?:\.\d+)?)$/i;

export function normalizePresetLabel(raw: string): string {
  return raw.trim().toLowerCase();
}

export function slugifyPresetLabel(raw: string): string {
  const s = raw
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
  return s === '' || s === 'default' ? 'custom_preset' : s;
}

function stripKnownExtension(filename: string): string {
  return filename.replace(/\.(stl|step|stp|3mf)$/i, '');
}

function extractFileStem(relPath?: string): string {
  if (!relPath) return '';
  const base = relPath.replace(/\\/g, '/').split('/').pop() ?? '';
  return base.replace(/\.py$/i, '').trim().toLowerCase();
}

function formatToken(raw: string): string {
  const lower = raw.toLowerCase();
  if (UPPERCASE_TOKENS[lower]) {
    return UPPERCASE_TOKENS[lower];
  }
  if (DIMENSION_RE.test(lower)) {
    return lower;
  }
  const rackMatch = RACK_UNIT_RE.exec(lower);
  if (rackMatch) {
    return `${rackMatch[1]}U`;
  }
  const fastenerMatch = METRIC_FASTENER_RE.exec(lower);
  if (fastenerMatch) {
    return `M${fastenerMatch[1]}`;
  }
  if (/^\d+(?:\.\d+)?$/.test(lower)) {
    return lower;
  }
  return lower.charAt(0).toUpperCase() + lower.slice(1);
}

/**
 * Derive a concise, human-friendly preset name from a model's dynamic
 * `export_filename(params)` stem by stripping the model prefix and formatting
 * CAD/material/dimension tokens (e.g. `can_lid_54mm_tpu` -> `54mm TPU`).
 *
 * Returns `''` when the filename carries no model-specific suffix.
 */
export function humanizeExportFilename(
  exportFilename: string,
  modelName?: string,
  modelRelPath?: string
): string {
  const cleaned = stripKnownExtension(exportFilename.trim());
  if (!cleaned) return '';

  const candidateStems = new Set<string>();
  const fileStem = extractFileStem(modelRelPath);
  if (fileStem) candidateStems.add(fileStem);
  if (modelName) {
    const nameSlug = modelName
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, '_')
      .replace(/^_+|_+$/g, '');
    if (nameSlug) candidateStems.add(nameSlug);
  }

  let remainder = cleaned;
  const lowerCleaned = cleaned.toLowerCase();
  // Match longest candidate stem first
  const sortedStems = [...candidateStems].sort((a, b) => b.length - a.length);
  for (const stem of sortedStems) {
    if (lowerCleaned === stem) {
      return '';
    }
    if (lowerCleaned.startsWith(`${stem}_`) || lowerCleaned.startsWith(`${stem}-`)) {
      remainder = cleaned.slice(stem.length + 1);
      break;
    }
  }

  const tokens = remainder
    .split(/[_\s]+/)
    .map((t) => t.trim())
    .filter(Boolean);

  if (tokens.length === 0) return '';

  return tokens.map(formatToken).join(' ');
}

/**
 * Resolve a collision-free preset label against all existing presets.
 * Avoids ` (User)` because "User" is a Preset Type scope; tries
 * ` (Modified)` first and then numeric suffixes ` (2)`, ` (3)`, ...
 */
export function resolveUniquePresetName(
  baseName: string,
  existingPresets: readonly Preset[],
  fallbackBase = 'Custom Preset'
): string {
  const base = baseName.trim() || fallbackBase;
  const existingLabels = new Set<string>();
  const existingKeys = new Set<string>();

  for (const p of existingPresets) {
    existingLabels.add(normalizePresetLabel(p.label));
    if (p.key !== 'default') {
      existingKeys.add(p.key.toLowerCase());
    }
  }

  const collides = (candidate: string): boolean => {
    const norm = normalizePresetLabel(candidate);
    const slug = slugifyPresetLabel(candidate);
    return existingLabels.has(norm) || existingKeys.has(slug);
  };

  if (!collides(base)) {
    return base;
  }

  const modifiedCandidate = `${base} (Modified)`;
  if (!collides(modifiedCandidate)) {
    return modifiedCandidate;
  }

  for (let i = 2; i < 1000; i++) {
    const numbered = `${base} (${i})`;
    if (!collides(numbered)) {
      return numbered;
    }
  }
  return `${base} (${Date.now()})`;
}

/**
 * Compute a smart collision-free starting name when saving a new preset from
 * the Base model configuration:
 * 1. Humanized dynamic suffix from `export_filename(params)` (e.g. `54mm TPU`)
 * 2. Custom `@default_preset(...)` label if non-generic
 * 3. Model display name (e.g. `Spool Cap`)
 * 4. `'Custom Preset'`
 */
export function suggestNewPresetName(options: {
  exportFilename: string;
  modelName?: string;
  modelRelPath?: string;
  basePresetLabel?: string;
  existingPresets: readonly Preset[];
}): string {
  const dynamicBase = humanizeExportFilename(
    options.exportFilename,
    options.modelName,
    options.modelRelPath
  );
  if (dynamicBase) {
    return resolveUniquePresetName(dynamicBase, options.existingPresets);
  }
  const baseLabel = options.basePresetLabel?.trim() ?? '';
  if (baseLabel && baseLabel.toLowerCase() !== 'default') {
    return resolveUniquePresetName(baseLabel, options.existingPresets);
  }
  const modelName = options.modelName?.trim() ?? '';
  if (modelName) {
    return resolveUniquePresetName(modelName, options.existingPresets);
  }
  return resolveUniquePresetName('Custom Preset', options.existingPresets);
}
