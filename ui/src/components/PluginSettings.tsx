import { useEffect, useMemo, useState } from 'react';
import { X, Settings, AlertCircle, CheckCircle2 } from 'lucide-react';

// A plugin describes its own settings form (GET /api/plugins/{name}/settings,
// proxied from the plugin's GET /settings), so this component renders any
// plugin without knowing what it configures.
import { changedValues, type Edits, type SettingField, type SettingsForm } from './pluginSettingsModel';

const inputCls = 'w-full bg-black/40 border border-white/10 rounded px-3 py-2 text-sm text-gray-200 focus:outline-none focus:border-blue-500/50';

export function PluginSettings({ plugin, onClose }: { plugin: { name: string; settings_cmd?: string[] }; onClose: () => void }) {
  const [form, setForm] = useState<SettingsForm | null>(null);
  const [edits, setEdits] = useState<Edits>({});
  const [loadError, setLoadError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

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
      .then(f => alive && setForm(f))
      .catch(e => alive && setLoadError(String(e.message || e)));
    return () => { alive = false; };
  }, [url, plugin.settings_cmd]);

  const groups = useMemo(() => {
    const g: Record<string, SettingField[]> = {};
    for (const f of form?.fields ?? []) (g[f.group || 'General'] ||= []).push(f);
    return g;
  }, [form]);

  const valueOf = (f: SettingField) => (f.key in edits ? edits[f.key] : f.value ?? '');
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
        body: JSON.stringify({ values: pending }),
      });
      const body = await res.json().catch(() => ({}));
      if (res.status === 422 && body.errors) {
        setFieldErrors(body.errors);
        setNotice(null);
        return;
      }
      if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
      // Adopt the saved values as the new baseline.
      setForm(f => f && { ...f, fields: f.fields.map(x => x.key in pending ? { ...x, value: pending[x.key] } : x) });
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
          {Object.entries(groups).map(([group, fields]) => (
            <fieldset key={group} className="space-y-3">
              <legend className="text-[11px] uppercase tracking-wider text-gray-500 mb-1">{group}</legend>
              {fields.map(f => {
                const id = `${plugin.name}-${f.key}`;
                const v = valueOf(f);
                const known = f.options?.some(o => o.value === String(v));
                return (
                  <div key={f.key}>
                    <label htmlFor={id} className="block text-xs text-gray-300 mb-1">{f.label}</label>
                    {f.type === 'select' ? (
                      <select id={id} className={inputCls} value={String(v)} onChange={e => setEdits({ ...edits, [f.key]: e.target.value })}>
                        {!known && <option value={String(v)}>{String(v) || '(unset)'}</option>}
                        {f.options?.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
                      </select>
                    ) : (
                      <>
                        <input
                          id={id}
                          className={inputCls}
                          type={f.type === 'number' ? 'number' : 'text'}
                          step={f.type === 'number' ? 'any' : undefined}
                          min={f.min}
                          max={f.max}
                          list={f.type === 'path' && f.options?.length ? `${id}-opts` : undefined}
                          value={String(v)}
                          placeholder={f.default !== undefined ? String(f.default) : undefined}
                          onChange={e => setEdits({ ...edits, [f.key]: e.target.value })}
                        />
                        {f.type === 'path' && f.options?.length ? (
                          <datalist id={`${id}-opts`}>{f.options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}</datalist>
                        ) : null}
                      </>
                    )}
                    {f.help && <p className="text-[11px] text-gray-500 mt-1">{f.help}</p>}
                    {fieldErrors[f.key] && <p className="text-[11px] text-red-400 mt-1">{fieldErrors[f.key]}</p>}
                  </div>
                );
              })}
            </fieldset>
          ))}
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
