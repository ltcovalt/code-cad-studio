export const DEFAULT_THEME = 'studio';

export const LIGHT_THEMES = new Set(['light', 'emerald']);

export const DAISY_THEMES: ReadonlyArray<{ id: string; label: string }> = [
  { id: 'studio', label: 'Studio' },
  { id: 'dark', label: 'Dark' },
  { id: 'light', label: 'Light' },
  { id: 'dracula', label: 'Dracula' },
  { id: 'night', label: 'Night' },
  { id: 'emerald', label: 'Emerald' },
];
