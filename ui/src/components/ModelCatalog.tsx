import { useCallback, useEffect, useRef, useState } from 'react';
import { Download, CheckCircle2, AlertCircle, Lock, RotateCw } from 'lucide-react';
import { finished, grouped, percent, progressText, type CatalogModel } from './modelCatalogModel';

// Lists a model catalog (gleann's own at /api/models/catalog, or a plugin's at
// /api/plugins/{name}/models) and downloads from it. Downloads run on the
// server, so they continue when this view is closed.
export function ModelCatalog({ url, groups, selected, onUse, onInstalled }: {
  url: string;
  groups?: string[];                 // show only these groups
  selected?: string;                 // the value currently in use
  onUse?: (value: string) => void;   // offer "Use" for installed models
  onInstalled?: (ids: string[]) => void; // a download finished
}) {
  const [models, setModels] = useState<CatalogModel[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const prev = useRef<CatalogModel[]>([]);
  // A ref, so an inline callback does not re-create load on every render.
  const installedCb = useRef(onInstalled);
  installedCb.current = onInstalled;

  const load = useCallback(async () => {
    try {
      const res = await fetch(url);
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
      const next: CatalogModel[] = body.models ?? [];
      const done = finished(prev.current, next);
      prev.current = next;
      setModels(next);
      setError(null);
      if (done.length) installedCb.current?.(done);
    } catch (e: any) {
      setError(String(e.message || e));
    }
  }, [url]);

  useEffect(() => { load(); }, [load]);

  // Poll while something is downloading.
  const busy = models?.some(m => m.state === 'downloading');
  useEffect(() => {
    if (!busy) return;
    const t = setInterval(load, 1000);
    return () => clearInterval(t);
  }, [busy, load]);

  const download = async (m: CatalogModel) => {
    const res = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id: m.id }) });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      setError(body.error || `HTTP ${res.status}`);
    }
    load();
  };

  if (error && !models) return <p className="text-[11px] text-red-400">Could not list the models: {error}</p>;
  if (!models) return <p className="text-[11px] text-gray-500">Loading models…</p>;

  return (
    <div className="space-y-3">
      {error && <p className="text-[11px] text-red-400">{error}</p>}
      {grouped(models, groups).map(([group, list]) => (
        <div key={group}>
          {(!groups || groups.length > 1) && <p className="text-[10px] uppercase tracking-wider text-gray-500 mb-1">{group}</p>}
          <ul className="space-y-1">
            {list.map(m => {
              const inUse = !!selected && !!m.value && m.value === selected;
              const p = percent(m);
              return (
                <li key={m.id} data-model={m.id}
                  className={`rounded border px-3 py-2 text-xs ${inUse ? 'border-blue-500/50 bg-blue-500/10' : 'border-white/10 bg-black/20'}`}>
                  <div className="flex items-center gap-2">
                    <span className="flex-1 text-gray-200">
                      {m.name}
                      {m.size && <span className="text-gray-500"> · {m.size}</span>}
                      {m.note && <span className="text-gray-500"> · {m.note}</span>}
                    </span>
                    {m.state === 'installed' && (onUse && m.value ? (
                      inUse
                        ? <span className="text-blue-300 flex items-center gap-1"><CheckCircle2 className="w-3.5 h-3.5" /> in use</span>
                        : <button type="button" onClick={() => onUse(m.value!)} className="px-2 py-0.5 rounded bg-white/5 hover:bg-white/10 border border-white/10 text-gray-200">Use</button>
                    ) : <span className="text-emerald-400 flex items-center gap-1"><CheckCircle2 className="w-3.5 h-3.5" /> installed</span>)}
                    {(m.state === 'available' || m.state === 'failed') && (
                      <button type="button" onClick={() => download(m)} aria-label={`Download ${m.name}`}
                        className="flex items-center gap-1 px-2 py-0.5 rounded bg-blue-600/30 hover:bg-blue-600/50 border border-blue-500/30 text-blue-200">
                        {m.state === 'failed' ? <RotateCw className="w-3 h-3" /> : <Download className="w-3 h-3" />}
                        {m.state === 'failed' ? 'Retry' : 'Download'}
                      </button>
                    )}
                    {m.state === 'blocked' && <span className="text-amber-400 flex items-center gap-1" title={m.blocked}><Lock className="w-3 h-3" /> {m.blocked}</span>}
                    {m.state === 'downloading' && <span className="text-blue-300 tabular-nums">{progressText(m)}</span>}
                  </div>
                  {m.state === 'downloading' && (
                    <div className="mt-1.5 h-1 rounded bg-white/10 overflow-hidden">
                      <div className={`h-full bg-blue-500 ${p === null ? 'animate-pulse w-full opacity-50' : ''}`} style={p === null ? undefined : { width: `${p}%` }} />
                    </div>
                  )}
                  {m.state === 'failed' && <p className="mt-1 text-red-400 flex items-center gap-1"><AlertCircle className="w-3 h-3" /> {m.error}</p>}
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </div>
  );
}
