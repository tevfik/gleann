import { useEffect, useState } from 'react';
import { X, Settings, AlertCircle, CheckCircle2, RotateCw } from 'lucide-react';

// A plugin describes its own settings form (GET /api/plugins/{name}/settings,
// proxied from the plugin's GET /settings), so this component renders any
// plugin without knowing what it configures.
import { ModelCatalog } from './ModelCatalog';
import { changedValues, currentValue, isVisible, type Edits, type SettingField, type SettingsForm } from './pluginSettingsModel';

const CUSTOM = '__custom__'; // the select value of the "Custom…" entry
const inputCls = 'w-full bg-black/40 border border-white/10 rounded px-3 py-2 text-sm text-gray-200 focus:outline-none focus:border-blue-500/50';

export function PluginSettings({ plugin, onClose }: { plugin: { name: string; settings_cmd?: string[] }; onClose: () => void }) {
  const [form, setForm] = useState<SettingsForm | null>(null);
  const [edits, setEdits] = useState<Edits>({});
  const [loadError, setLoadError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [conflict, setConflict] = useState<string | null>(null);
  const [reloads, setReloads] = useState(0);
  // Selects with allow_custom whose "Custom…" entry is chosen.
  const [custom, setCustom] = useState<Record<string, boolean>>({});

  const url = `/api/plugins/${encodeURIComponent(plugin.name)}/settings`;

  useEffect(() => {
    let alive = true;
    fetch(url)
      .then(async res => {
        const body = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(res.status === 501
          ? `This plugin has no web settings.${plugin.settings_cmd?.length ? ` Run in a terminal: ${plugin.settings_cmd.join(' ')}` : ''}`
          : body.error || `HTTP ${res.status}`);
        return body as SettingsForm;
      })
      .then(f => {
        if (!alive) return;
        setForm(f);
        setEdits({});
        setConflict(null);
        setLoadError(null);
      })
      .catch(e => alive && setLoadError(String(e.message || e)));
    return () => { alive = false; };
  }, [url, plugin.settings_cmd, reloads]);

  // Fields hidden by visible_if follow the edits, so they are grouped on every render.
  const groups: Record<string, SettingField[]> = {};
  for (const f of form?.fields ?? []) {
    if (isVisible(f, form!.fields, edits)) (groups[f.group || 'General'] ||= []).push(f);
  }

  const valueOf = (f: SettingField) => currentValue(f, edits);

  // After a download the plugin lists the new model among a field's options:
  // re-read the form but keep what the user is editing.
  const refreshForm = async () => {
    const res = await fetch(url);
    if (res.ok) setForm(await res.json());
  };
  const pending = form ? changedValues(form.fields, edits) : {};
  const dirty = Object.keys(pending).length > 0;

  const save = async () => {
    setSaving(true);
    setFieldErrors({});
    setNotice(null);
    try {
      const res = await fetch(url, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ values: pending, revision: form?.revision }),
      });
      const body = await res.json().catch(() => ({}));
      if (res.status === 422 && body.errors) {
        setFieldErrors(body.errors);
        setNotice(null);
        return;
      }
      if (res.status === 409) {
        setConflict(body.error || 'The settings were changed elsewhere. Reload to see them.');
        return;
      }
      if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
      // Adopt the saved values as the new baseline (a secret stays hidden, now set).
      setForm(f => f && {
        ...f,
        revision: body.revision ?? f.revision,
        fields: f.fields.map(x => !(x.key in pending) ? x
          : x.type === 'secret' ? { ...x, value: '', is_set: pending[x.key] !== '' }
          : { ...x, value: pending[x.key] }),
      });
      setEdits({});
      setNotice(body.restart_required ? (form?.restart_note || 'Saved. Restart the plugin to apply.') : 'Saved.');
    } catch (e: any) {
      setFieldErrors({});
      setNotice(null);
      setLoadError(String(e.message || e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4" role="dialog" aria-label={`${plugin.name} settings`}>
      <div className="bg-[#12141a] border border-white/10 rounded-xl w-full max-w-2xl max-h-[90vh] flex flex-col shadow-2xl">
        <div className="flex items-center justify-between px-5 py-4 border-b border-white/10">
          <h3 className="flex items-center gap-2 text-sm font-semibold text-gray-100">
            <Settings className="w-4 h-4 text-gray-400" /> {plugin.name} settings
          </h3>
          <button onClick={onClose} aria-label="Close" className="text-gray-400 hover:text-white"><X className="w-4 h-4" /></button>
        </div>

        <div className="overflow-y-auto px-5 py-4 space-y-5">
          {!form && !loadError && <p className="text-sm text-gray-400">Loading…</p>}
          {loadError && (
            <p className="flex gap-2 text-sm text-red-300 bg-red-500/10 border border-red-500/20 rounded p-3">
              <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" /> {loadError}
            </p>
          )}
          {conflict && (
            <p className="flex items-center gap-2 text-sm text-amber-300 bg-amber-500/10 border border-amber-500/20 rounded p-3">
              <AlertCircle className="w-4 h-4 shrink-0" /> <span className="flex-1">{conflict}</span>
              <button onClick={() => setReloads(n => n + 1)} className="flex items-center gap-1 text-xs bg-white/5 hover:bg-white/10 border border-white/10 rounded px-2 py-1">
                <RotateCw className="w-3 h-3" /> Reload
              </button>
            </p>
          )}
          {fieldErrors[''] && <p className="text-sm text-red-300">{fieldErrors['']}</p>}
          {Object.entries(groups).map(([group, fields]) => (
            <fieldset key={group} className="space-y-3">
              <legend className="text-[11px] uppercase tracking-wider text-gray-500 mb-1">{group}</legend>
              {fields.map(f => {
                const id = `${plugin.name}-${f.key}`;
                const v = valueOf(f);
                const known = f.options?.some(o => o.value === String(v));
                const showCustom = f.type === 'select' && f.allow_custom && (custom[f.key] || (!known && v !== ''));
                return (
                  <div key={f.key}>
                    {f.type !== 'bool' && <label htmlFor={id} className="block text-xs text-gray-300 mb-1">{f.label}</label>}
                    {f.type === 'bool' ? (
                      <label htmlFor={id} className="flex items-center gap-2 text-xs text-gray-300 cursor-pointer">
                        <input id={id} type="checkbox" className="accent-blue-500" checked={v === true || v === 'true'}
                          onChange={e => setEdits({ ...edits, [f.key]: e.target.checked })} />
                        {f.label}
                      </label>
                    ) : f.type === 'select' && !f.allow_custom ? (
                      <select id={id} className={inputCls} value={String(v)} onChange={e => setEdits({ ...edits, [f.key]: e.target.value })}>
                        {!known && <option value={String(v)}>{String(v) || '(unset)'}</option>}
                        {f.options?.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
                      </select>
                    ) : f.type === 'select' ? (
                      // allow_custom: the choices, then "Custom…" which opens a text field.
                      <>
                        <select id={id} className={inputCls} value={showCustom ? CUSTOM : String(v)}
                          onChange={e => {
                            const pick = e.target.value;
                            setCustom({ ...custom, [f.key]: pick === CUSTOM });
                            if (pick !== CUSTOM) setEdits({ ...edits, [f.key]: pick });
                          }}>
                          {f.options?.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
                          <option value={CUSTOM}>Custom…</option>
                        </select>
                        {showCustom && (
                          <input id={`${id}-custom`} className={`${inputCls} mt-2`} value={String(v)} autoFocus
                            placeholder="type a value" onChange={e => setEdits({ ...edits, [f.key]: e.target.value })} />
                        )}
                      </>
                    ) : (
                      <>
                        <input
                          id={id}
                          className={inputCls}
                          type={f.type === 'number' ? 'number' : f.type === 'secret' ? 'password' : 'text'}
                          step={f.type === 'number' ? (f.step ?? 'any') : undefined}
                          min={f.min}
                          max={f.max}
                          autoComplete={f.type === 'secret' ? 'new-password' : undefined}
                          list={f.options?.length ? `${id}-opts` : undefined}
                          value={String(v)}
                          placeholder={f.type === 'secret'
                            ? (f.is_set ? '•••••• (set — type to replace)' : '(not set)')
                            : f.placeholder ? `auto: ${f.placeholder}` : f.default !== undefined ? String(f.default) : undefined}
                          onChange={e => setEdits({ ...edits, [f.key]: e.target.value })}
                        />
                        {f.options?.length ? (
                          <datalist id={`${id}-opts`}>{f.options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}</datalist>
                        ) : null}
                      </>
                    )}
                    {f.options_error && <p className="text-[11px] text-amber-400 mt-1">Could not list the choices ({f.options_error}); pick a suggestion or type a value.</p>}
                    {f.help && <p className="text-[11px] text-gray-500 mt-1">{f.help}</p>}
                    {fieldErrors[f.key] && <p className="text-[11px] text-red-400 mt-1">{fieldErrors[f.key]}</p>}
                  </div>
                );
              })}
            </fieldset>
          ))}
          {form?.models && (
            <fieldset className="space-y-2">
              <legend className="text-[11px] uppercase tracking-wider text-gray-500 mb-1">Models</legend>
              <p className="text-[11px] text-gray-500">Downloads run on the server and continue if you close this window.</p>
              {/* keyed by revision: a saved setting (e.g. a license) can unlock a model */}
              <ModelCatalog key={form.revision} url={`/api/plugins/${encodeURIComponent(plugin.name)}/models`} onInstalled={refreshForm} />
            </fieldset>
          )}
        </div>

        <div className="flex items-center justify-between gap-3 px-5 py-3 border-t border-white/10">
          <p className="text-[11px] text-emerald-300 flex items-center gap-1.5 min-h-4">
            {notice && <><CheckCircle2 className="w-3.5 h-3.5 shrink-0" /> {notice}</>}
          </p>
          <div className="flex gap-2 shrink-0">
            <button onClick={onClose} className="bg-white/5 hover:bg-white/10 text-gray-300 border border-white/10 px-3 py-1.5 rounded text-xs">Close</button>
            <button
              onClick={save}
              disabled={!dirty || saving || !form}
              className="bg-blue-600/30 hover:bg-blue-600/50 disabled:opacity-40 disabled:cursor-not-allowed text-blue-200 border border-blue-500/30 px-3 py-1.5 rounded text-xs font-medium"
            >
              {saving ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
