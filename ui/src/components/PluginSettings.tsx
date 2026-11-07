import { X, Settings } from 'lucide-react';

// A plugin describes its own settings form (GET /api/plugins/{name}/settings,
// proxied from the plugin's GET /settings); SettingsForm renders it, the same
// component that renders gleann's own settings.
import { SettingsForm } from './SettingsForm';

export function PluginSettings({ plugin, onClose }: { plugin: { name: string; settings_cmd?: string[] }; onClose: () => void }) {
  const base = `/api/plugins/${encodeURIComponent(plugin.name)}`;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4" role="dialog" aria-label={`${plugin.name} settings`}>
      <div className="bg-[#12141a] border border-white/10 rounded-xl w-full max-w-2xl max-h-[90vh] flex flex-col shadow-2xl">
        <div className="flex items-center justify-between px-5 py-4 border-b border-white/10">
          <h3 className="flex items-center gap-2 text-sm font-semibold text-gray-100">
            <Settings className="w-4 h-4 text-gray-400" /> {plugin.name} settings
          </h3>
          <button onClick={onClose} aria-label="Close" className="text-gray-400 hover:text-white"><X className="w-4 h-4" /></button>
        </div>
        <SettingsForm
          url={`${base}/settings`}
          catalogUrl={`${base}/models`}
          idPrefix={plugin.name}
          bodyClass="overflow-y-auto px-5 py-4"
          notFound={`This plugin has no web settings.${plugin.settings_cmd?.length ? ` Run in a terminal: ${plugin.settings_cmd.join(' ')}` : ''}`}
          footer={bar => (
            <div className="flex items-center gap-3 px-5 py-3 border-t border-white/10">
              <div className="flex-1">{bar}</div>
              <button onClick={onClose} className="bg-white/5 hover:bg-white/10 text-gray-300 border border-white/10 px-3 py-1.5 rounded text-xs">Close</button>
            </div>
          )}
        />
      </div>
    </div>
  );
}
