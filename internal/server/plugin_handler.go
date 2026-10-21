package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/background"
	"github.com/tevfik/gleann/pkg/gleann"
)

// handleListPlugins returns the plugin catalog and current install status.
// GET /api/plugins
func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	catalog := gleann.FetchPluginCatalog()

	installedPlugins, err := gleann.LoadPlugins()
	if err != nil {
		installedPlugins = &gleann.PluginRegistry{}
	}

	type pluginResponse struct {
		gleann.PluginMeta
		Status string `json:"status"` // "not_installed", "installed", "running"
	}

	var response []pluginResponse
	for _, meta := range catalog {
		status := "not_installed"
		for _, installed := range installedPlugins.Plugins {
			if installed.Name == meta.Name {
				status = "installed"
				break
			}
		}

		response = append(response, pluginResponse{
			PluginMeta: meta,
			Status:     status,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"plugins": response})
}

// handleInstallPlugin spawns a background task to install a plugin.
// POST /api/plugins/{name}/install
func (s *Server) handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid plugin name: "+err.Error())
		return
	}

	catalog := gleann.FetchPluginCatalog()
	var meta *gleann.PluginMeta
	for i, c := range catalog {
		if c.Name == name {
			meta = &catalog[i]
			break
		}
	}

	if meta == nil {
		writeError(w, http.StatusNotFound, "plugin not found in catalog")
		return
	}

	taskID := s.bgManager.Submit(
		background.TaskTypeCustom,
		func(progress func(pct float64, msg string)) error {
			progressCh := make(chan string, 100)

			var installErr error
			var result string

			go func() {
				defer close(progressCh)
				result, installErr = gleann.InstallPlugin(*meta, progressCh)
			}()

			for msg := range progressCh {
				progress(0.5, msg) // approximate progress
			}

			if installErr != nil {
				return installErr
			}

			progress(1.0, fmt.Sprintf("Completed: %s", result))
			return nil
		},
	)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"task_id": taskID,
		"message": "installation started in background",
	})
}

// handleUninstallPlugin uninstalls a plugin.
// DELETE /api/plugins/{name}
func (s *Server) handleUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid plugin name: "+err.Error())
		return
	}

	result, err := gleann.UninstallPlugin(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}

// pluginManager returns the manager used to reach (and, when needed, start)
// registered plugins for settings requests.
func (s *Server) pluginManager() (*gleann.PluginManager, error) {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	if s.pluginMgr == nil {
		m, err := gleann.NewPluginManager()
		if err != nil {
			return nil, err
		}
		s.pluginMgr = m
	}
	return s.pluginMgr, nil
}

// closePluginManager stops plugins this server had to start.
func (s *Server) closePluginManager() {
	s.pluginMu.Lock()
	m := s.pluginMgr
	s.pluginMgr = nil
	s.pluginMu.Unlock()
	if m != nil {
		m.Close()
	}
}

// handlePluginSettings proxies a plugin's own settings form so the web UI can
// render and save it without knowing anything about the plugin:
//
//	GET /api/plugins/{name}/settings  -> the plugin's GET /settings (fields + values)
//	PUT /api/plugins/{name}/settings  -> the plugin's PUT /settings ({"values":{...}})
//
// The plugin is started first when it is registered but not running. Only the
// /settings path of the registered plugin URL is ever contacted.
func (s *Server) handlePluginSettings(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := gleann.ValidateName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid plugin name: "+err.Error())
		return
	}
	reg, err := gleann.LoadPlugins()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var plugin *gleann.Plugin
	for i := range reg.Plugins {
		if reg.Plugins[i].Name == name {
			plugin = &reg.Plugins[i]
			break
		}
	}
	if plugin == nil || plugin.URL == "" {
		writeError(w, http.StatusNotFound, "plugin is not installed")
		return
	}

	mgr, err := s.pluginManager()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := mgr.EnsurePluginRunning(plugin); err != nil {
		writeError(w, http.StatusBadGateway, "plugin is not running and could not be started: "+err.Error())
		return
	}

	var body io.Reader
	if r.Method == http.MethodPut {
		body = http.MaxBytesReader(w, r.Body, 1<<20)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, strings.TrimRight(plugin.URL, "/")+"/settings", body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "plugin request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		writeError(w, http.StatusNotImplemented, "this plugin has no web-editable settings")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 4<<20))
}
