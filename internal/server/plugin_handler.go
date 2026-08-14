package server

import (
	"fmt"
	"net/http"

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
	if name == "" {
		writeError(w, http.StatusBadRequest, "plugin name required")
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
	if name == "" {
		writeError(w, http.StatusBadRequest, "plugin name required")
		return
	}

	result, err := gleann.UninstallPlugin(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}
