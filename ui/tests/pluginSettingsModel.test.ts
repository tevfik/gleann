// Run with: npm test  (node's built-in runner; no extra dependencies)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { changedValues, settingPayload, type SettingField } from '../src/components/pluginSettingsModel.ts';

const fields: SettingField[] = [
  { key: 'language', label: 'Language', type: 'select', value: 'tr' },
  { key: 'hotkey', label: 'Hotkey', type: 'text', value: 'ctrl+shift+space' },
  { key: 'speaker_threshold', label: 'Threshold', type: 'number', value: 0 },
  { key: 'refine_model', label: 'Refine', type: 'path', value: null },
];

test('nothing is sent when nothing changed', () => {
  assert.deepEqual(changedValues(fields, {}), {});
  assert.deepEqual(changedValues(fields, { language: 'tr', speaker_threshold: '0', refine_model: '' }), {});
});

test('only changed fields are sent, numbers as numbers', () => {
  assert.deepEqual(
    changedValues(fields, { language: 'en', hotkey: 'ctrl+shift+space', speaker_threshold: '0.5' }),
    { language: 'en', speaker_threshold: 0.5 },
  );
});

test('clearing a field sends an empty string', () => {
  assert.deepEqual(changedValues(fields, { hotkey: '' }), { hotkey: '' });
});

test('settingPayload follows the field type', () => {
  assert.equal(settingPayload(fields[2], '0.35'), 0.35);
  assert.equal(settingPayload(fields[0], 'en'), 'en');
});
