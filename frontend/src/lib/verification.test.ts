import { describe, expect, it } from 'vitest';
import {
  formatVerificationTooltip,
  getPresetVerification,
  getVerificationMeta,
  hasVerificationDetails,
} from './verification';
import type { Preset, VerificationStatus } from './types';

describe('verification helpers', () => {
  it('getVerificationMeta falls back to unverified on unknown status', () => {
    expect(getVerificationMeta('verified').label).toBe('Verified');
    expect(getVerificationMeta('unknown' as VerificationStatus).value).toBe('unverified');
  });

  it('getPresetVerification trims notes and normalizes invalid statuses', () => {
    expect(getPresetVerification(null)).toEqual({ status: 'unverified', note: '' });

    const validPreset: Preset = {
      key: 'p1',
      label: 'P1',
      description: '',
      source: 'project',
      verification: { status: 'usable', note: '  Tight fit on PETG  ' },
      params: {},
    };
    expect(getPresetVerification(validPreset)).toEqual({
      status: 'usable',
      note: 'Tight fit on PETG',
    });

    const invalidPreset: Preset = {
      ...validPreset,
      verification: { status: 'bogus' as VerificationStatus, note: 'note' },
    };
    expect(getPresetVerification(invalidPreset)).toEqual({
      status: 'unverified',
      note: 'note',
    });
  });

  it('hasVerificationDetails detects non-default status or non-empty note', () => {
    expect(hasVerificationDetails(null)).toBe(false);
    expect(hasVerificationDetails({ status: 'unverified', note: '   ' })).toBe(false);
    expect(hasVerificationDetails({ status: 'unverified', note: 'Tested draft' })).toBe(true);
    expect(hasVerificationDetails({ status: 'verified', note: '' })).toBe(true);
  });

  it('formatVerificationTooltip formats label, note, and modified state', () => {
    expect(formatVerificationTooltip('verified', 'Printed 0.2mm', true)).toBe(
      'Verification: Verified\nPrinted 0.2mm\nLive parameters differ from this preset'
    );
    expect(formatVerificationTooltip('unusable', '', false)).toBe('Verification: Unusable');
  });
});
