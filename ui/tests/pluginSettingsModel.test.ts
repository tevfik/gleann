// Run with: npm test  (node's built-in runner; no extra dependencies)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { changedValues, currentValue, isVisible, settingPayload, type SettingField } from '../src/components/pluginSettingsModel.ts';

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

// schema_version 2 (github.com/tevfik/gleann/modules/settings)
const v2: SettingField[] = [
  { key: 'match', label: 'Only my voice', type: 'bool', value: false },
  { key: 'threshold', label: 'Threshold', type: 'number', value: 0.45, visible_if: { key: 'match', equals: true } },
  { key: 'token', label: 'Token', type: 'secret', value: '', is_set: true },
];

test('switches are sent as booleans, and only when changed', () => {
  assert.deepEqual(changedValues(v2, { match: false }), {});
  assert.deepEqual(changedValues(v2, { match: true }), { match: true });
  assert.equal(settingPayload(v2[0], 'true'), true);
});

test('visible_if follows the edited value of the other field', () => {
  assert.equal(isVisible(v2[1], v2, {}), false);
  assert.equal(isVisible(v2[1], v2, { match: true }), true);
  assert.equal(isVisible(v2[0], v2, {}), true);
});

test('an untouched secret is never sent', () => {
  assert.deepEqual(changedValues(v2, { token: '' }), {});
  assert.deepEqual(changedValues(v2, { token: 'new' }), { token: 'new' });
  assert.equal(currentValue(v2[2], {}), '');
});

// model catalog
import { finished, grouped, percent, progressText, type CatalogModel } from '../src/components/modelCatalogModel.ts';

const cat = (id: string, state: CatalogModel['state'], group = 'g', progress?: CatalogModel['progress']): CatalogModel =>
  ({ id, name: id, group, installed: state === 'installed', state, progress });

test('catalog progress and grouping', () => {
  assert.equal(percent(cat('a', 'downloading', 'g', { done: 50, total: 200 })), 25);
  assert.equal(percent(cat('a', 'downloading', 'g', { message: 'file 1 of 3' })), null);
  assert.equal(progressText(cat('a', 'downloading', 'g', { message: 'file 1 of 3' })), 'file 1 of 3');
  assert.equal(progressText(cat('a', 'downloading', 'g', { done: 1 << 20, total: 4 << 20 })), '25% · 1 / 4 MB');
  const g = grouped([cat('a', 'available', 'LLMs'), cat('b', 'available', 'Embedding'), cat('c', 'installed', 'LLMs')], ['LLMs']);
  assert.deepEqual(g.map(([k, v]) => [k, v.map(m => m.id)]), [['LLMs', ['a', 'c']]]);
});

test('a finished download is noticed once', () => {
  const before = [cat('a', 'downloading'), cat('b', 'available')];
  assert.deepEqual(finished(before, [cat('a', 'installed'), cat('b', 'available')]), ['a']);
  assert.deepEqual(finished([cat('a', 'installed')], [cat('a', 'installed')]), []);
});
