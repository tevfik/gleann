// Pure model of a model catalog (github.com/tevfik/gleann/modules/settings
// Catalog: GET {"models":[...]}, POST {"id":...}), kept apart so it can be tested.
export interface CatalogModel {
  id: string;
  name: string;
  group?: string;
  size?: string;
  note?: string;
  value?: string; // what a setting stores to use this model
  installed: boolean;
  blocked?: string;
  state: 'installed' | 'available' | 'downloading' | 'failed' | 'blocked';
  progress?: { done?: number; total?: number; message?: string };
  error?: string;
}

// Percent done, or null when the size is unknown.
export const percent = (m: CatalogModel): number | null => {
  const { done = 0, total = 0 } = m.progress ?? {};
  return total > 0 ? Math.min(100, Math.floor((done / total) * 100)) : null;
};

// Short progress text: "42% · 120 / 466 MB", or the downloader's message.
export const progressText = (m: CatalogModel): string => {
  const p = percent(m);
  const mb = (n?: number) => Math.round((n ?? 0) / (1 << 20));
  if (p !== null) return `${p}% · ${mb(m.progress?.done)} / ${mb(m.progress?.total)} MB`;
  return m.progress?.message || 'downloading…';
};

// The catalog grouped in order of appearance, optionally limited to groups.
export const grouped = (models: CatalogModel[], only?: string[]): [string, CatalogModel[]][] => {
  const out = new Map<string, CatalogModel[]>();
  for (const m of models) {
    const g = m.group || 'Models';
    if (only && !only.includes(g)) continue;
    if (!out.has(g)) out.set(g, []);
    out.get(g)!.push(m);
  }
  return [...out.entries()];
};

// Ids that were downloading before and are installed now.
export const finished = (before: CatalogModel[], after: CatalogModel[]): string[] => {
  const was = new Set(before.filter(m => m.state === 'downloading').map(m => m.id));
  return after.filter(m => was.has(m.id) && m.state === 'installed').map(m => m.id);
};
