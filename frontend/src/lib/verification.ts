import type { Preset, PresetVerification, VerificationStatus } from './types';

export interface VerificationStatusMeta {
  value: VerificationStatus;
  label: string;
  hint: string;
  iconClass: string;
  selectedButtonClass: string;
}

export const VERIFICATION_STATUSES: readonly VerificationStatusMeta[] = [
  {
    value: 'unverified',
    label: 'Unverified',
    hint: 'Not yet print-tested',
    iconClass: 'text-base-content/50',
    selectedButtonClass: 'border-base-content/40 bg-base-200/80 text-base-content',
  },
  {
    value: 'unusable',
    label: 'Unusable',
    hint: 'Printed, failed or does not fit',
    iconClass: 'text-error',
    selectedButtonClass: 'border-error/60 bg-error/10 text-error',
  },
  {
    value: 'usable',
    label: 'Usable w/ Issues',
    hint: 'Printed and works with minor caveats',
    iconClass: 'text-warning',
    selectedButtonClass: 'border-warning/60 bg-warning/10 text-warning',
  },
  {
    value: 'verified',
    label: 'Verified',
    hint: 'Print-tested and confirmed working',
    iconClass: 'text-success',
    selectedButtonClass: 'border-success/60 bg-success/10 text-success',
  },
] as const;

const STATUS_MAP = new Map<VerificationStatus, VerificationStatusMeta>(
  VERIFICATION_STATUSES.map((item) => [item.value, item])
);

export function getVerificationMeta(status: VerificationStatus): VerificationStatusMeta {
  return STATUS_MAP.get(status) ?? VERIFICATION_STATUSES[0];
}

export function getPresetVerification(preset: Preset | null | undefined): {
  status: VerificationStatus;
  note: string;
} {
  const raw = preset?.verification;
  if (!raw) {
    return { status: 'unverified', note: '' };
  }
  const status: VerificationStatus = STATUS_MAP.has(raw.status) ? raw.status : 'unverified';
  const note = (raw.note ?? '').trim();
  return { status, note };
}

export function hasVerificationDetails(v: PresetVerification | null | undefined): boolean {
  if (!v) return false;
  return v.status !== 'unverified' || Boolean((v.note ?? '').trim());
}

export function formatVerificationTooltip(
  status: VerificationStatus,
  note: string,
  modified = false
): string {
  const meta = getVerificationMeta(status);
  const parts: string[] = [`Verification: ${meta.label}`];
  if (note) {
    parts.push(note);
  }
  if (modified) {
    parts.push('Live parameters differ from this preset');
  }
  return parts.join('\n');
}
