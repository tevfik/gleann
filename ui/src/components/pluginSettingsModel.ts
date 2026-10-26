// Pure model of a plugin-described settings form (no React), kept apart so it can be tested.
// The form comes from github.com/tevfik/gleann/modules/settings (schema_version 2;
// version 1 forms lack the optional fields).
export interface SettingOption { value: string; label: string }
export type SettingValue = string | number | boolean;
export interface SettingField {
  key: string;
  label: string;
  type: 'select' | 'text' | 'path' | 'number' | 'bool' | 'secret';
  group?: string;
  help?: string;
  options?: SettingOption[];
  options_error?: string; // the run-time options could not be listed; options are suggestions
  allow_custom?: boolean; // select: values outside options are accepted
  value: SettingValue | null;
  is_set?: boolean; // secret: a value is stored (it is never sent)
  placeholder?: string; // what an empty value means right now (e.g. the library found)
  default?: SettingValue;
  min?: number;
  max?: number;
  step?: number;
  visible_if?: { key: string; equals: SettingValue };
  apply?: 'live' | 'restart';
}
export interface SettingsForm {
  schema_version?: number;
  title?: string;
  restart_note?: string;
  revision?: string; // sent back with a save, which is refused if the settings changed meanwhile
  models?: string; // path of the plugin's model catalog, when it offers downloads
  fields: SettingField[];
}

export type Edits = Record<string, SettingValue>;

// What to send for a field: numbers as numbers, switches as booleans, everything else as text.
export const settingPayload = (f: SettingField, edit: SettingValue): SettingValue => {
  if (f.type === 'number') return Number(edit);
  if (f.type === 'bool') return edit === true || edit === 'true';
  return String(edit);
};

// The value a field shows: the user's edit, else the stored value.
export const currentValue = (f: SettingField, edits: Edits): SettingValue =>
  f.key in edits ? edits[f.key] : f.value ?? (f.type === 'bool' ? false : '');

// A field with visible_if is shown only while the other field (as edited) has that value.
export const isVisible = (f: SettingField, fields: SettingField[], edits: Edits): boolean => {
  if (!f.visible_if) return true;
  const other = fields.find(x => x.key === f.visible_if!.key);
  return !!other && String(currentValue(other, edits)) === String(f.visible_if.equals);
};

// Only fields the user changed are sent, so a save never rewrites the rest.
export const changedValues = (fields: SettingField[], edits: Edits): Record<string, SettingValue> => {
  const out: Record<string, SettingValue> = {};
  for (const f of fields) {
    if (!(f.key in edits)) continue;
    if (String(edits[f.key]) === String(f.value ?? (f.type === 'bool' ? false : ''))) continue;
    out[f.key] = settingPayload(f, edits[f.key]);
  }
  return out;
};
