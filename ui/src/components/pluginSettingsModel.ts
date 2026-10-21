// Pure model of a plugin-described settings form (no React), kept apart so it can be tested.
export interface SettingOption { value: string; label: string }
export interface SettingField {
  key: string;
  label: string;
  type: 'select' | 'text' | 'path' | 'number';
  group?: string;
  help?: string;
  options?: SettingOption[];
  value: string | number | null;
  default?: string | number;
  min?: number;
  max?: number;
}
export interface SettingsForm { title?: string; restart_note?: string; fields: SettingField[] }

export type Edits = Record<string, string | number>;

// What to send for a field: numbers as numbers, everything else as text.
export const settingPayload = (f: SettingField, edit: string | number): string | number =>
  f.type === 'number' ? Number(edit) : String(edit);

// Only fields the user changed are sent, so a save never rewrites the rest.
export const changedValues = (fields: SettingField[], edits: Edits): Record<string, string | number> => {
  const out: Record<string, string | number> = {};
  for (const f of fields) {
    if (!(f.key in edits)) continue;
    if (String(edits[f.key]) === String(f.value ?? '')) continue;
    out[f.key] = settingPayload(f, edits[f.key]);
  }
  return out;
};

