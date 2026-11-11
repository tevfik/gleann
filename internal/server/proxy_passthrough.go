package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Pass-through RAG proxy (proxy_mode "passthrough", or GLEANN_PROXY_MODE=passthrough).
//
// The default proxy rebuilds the conversation through gleann's chat client, which keeps only each message's role
// and text and streams only the answer text. In pass-through mode the client's request goes to the LLM server
// unchanged (every field, e.g. chat_template_kwargs), except for the retrieved context, and the server's reply goes
// back byte for byte with its status code, so clients keep what their LLM server streams (reasoning, timing
// metrics, errors). POST /stop is forwarded too (some LLM servers stop the running generation with it).
//
// Indexes come from the model name ("gleann/<index>[,<index>...]", "@tag" allowed) or from the X-Gleann-Index
// header (the same list), and the search is the last user message's text. The passages are appended to the first
// system message, or added as one when there is none, because some chat templates accept only one system message
// at the start. The model name is replaced by llm_model when it named indexes. X-Gleann-Top-K and
// X-Gleann-Min-Score work as in the default mode; with several indexes the passages are ranked together by score
// and the best top-k kept. The reply carries X-Gleann-Sources, the passages in order as a percent-encoded JSON list
// of {"index", "source", "score"}.

// passthroughContextIntro opens the context the proxy adds to the system message.
const passthroughContextIntro = "Use the following context, retrieved from the user's documents, to answer. " +
	"If it does not contain the answer, say so.\n\nContext:\n"

// maxPassthroughBody bounds a proxied request body.
const maxPassthroughBody = 64 << 20

func (s *Server) proxyPassthrough() bool {
	if v := os.Getenv("GLEANN_PROXY_MODE"); v != "" {
		return strings.EqualFold(v, "passthrough")
	}
	return strings.EqualFold(s.cfg().ProxyMode, "passthrough")
}

// passthroughUpstream returns the LLM server's base URL (without /v1) and API key.
func (s *Server) passthroughUpstream() (string, string) {
	cfg := s.proxyLLMConfig()
	return strings.TrimSuffix(strings.TrimRight(cfg.BaseURL, "/"), "/v1"), cfg.APIKey
}

func (s *Server) passthroughChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPassthroughBody+1))
	if err != nil || len(body) > maxPassthroughBody {
		writeError(w, http.StatusBadRequest, "request body unreadable or too large")
		return
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	var messages []map[string]any
	if err := json.Unmarshal(req["messages"], &messages); err != nil || len(messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages array is required")
		return
	}
	var model string
	_ = json.Unmarshal(req["model"], &model)

	indexNames := parseIndexFromModel(model)
	if h := strings.TrimSpace(r.Header.Get("X-Gleann-Index")); h != "" {
		indexNames = parseIndexFromModel("gleann/" + h)
	}
	for _, n := range indexNames {
		if strings.HasPrefix(n, "@") {
			continue
		}
		if err := gleann.ValidateName(n); err != nil {
			writeError(w, http.StatusBadRequest, "invalid index name: "+err.Error())
			return
		}
	}

	if len(indexNames) > 0 {
		var searchOpts []gleann.SearchOption
		if v := r.Header.Get("X-Gleann-Top-K"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				searchOpts = append(searchOpts, gleann.WithTopK(n))
			}
		} else if s.cfg().SearchConfig.TopK > 0 {
			searchOpts = append(searchOpts, gleann.WithTopK(s.cfg().SearchConfig.TopK))
		}
		if v := r.Header.Get("X-Gleann-Min-Score"); v != "" {
			if f, err := strconv.ParseFloat(v, 32); err == nil {
				searchOpts = append(searchOpts, gleann.WithMinScore(float32(f)))
			}
		}
		block, sources, err := s.retrieveProxyContext(r.Context(), lastUserText(messages), indexNames, searchOpts)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "RAG retrieval failed: "+err.Error())
			return
		}
		if block != "" {
			messages = injectContext(messages, passthroughContextIntro+block)
			raw, _ := json.Marshal(messages)
			req["messages"] = raw
			if list, err := json.Marshal(sources); err == nil {
				w.Header().Set("X-Gleann-Sources", url.QueryEscape(string(list)))
			}
		}
	}
	if strings.HasPrefix(model, "gleann/") {
		upstreamModel := s.cfg().LLMModel
		if upstreamModel == "" {
			upstreamModel = model
		}
		req["model"], _ = json.Marshal(upstreamModel)
	}
	out, _ := json.Marshal(req)

	base, apiKey := s.passthroughUpstream()
	s.forwardPassthrough(w, r, base+"/v1/chat/completions", out, apiKey)
}

// handleProxyStop forwards POST /stop to the LLM server (pass-through mode only).
func (s *Server) handleProxyStop(w http.ResponseWriter, r *http.Request) {
	if !s.proxyPassthrough() {
		writeError(w, http.StatusNotFound, "POST /stop is forwarded in proxy_mode passthrough only")
		return
	}
	base, apiKey := s.passthroughUpstream()
	s.forwardPassthrough(w, r, base+"/stop", nil, apiKey)
}

// forwardPassthrough sends body to target and copies the answer (status, content headers, body as it arrives).
// The upstream request ends when the client goes away.
func (s *Server) forwardPassthrough(w http.ResponseWriter, r *http.Request, target string, body []byte, apiKey string) {
	up, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body != nil {
		up.Header.Set("Content-Type", "application/json")
	}
	if accept := r.Header.Get("Accept"); accept != "" {
		up.Header.Set("Accept", accept)
	}
	if apiKey != "" {
		up.Header.Set("Authorization", "Bearer "+apiKey)
	}
	// No client timeout: a streamed answer can run for many minutes; the request context bounds it.
	resp, err := http.DefaultTransport.RoundTrip(up)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		writeError(w, http.StatusBadGateway, fmt.Sprintf("LLM server unreachable: %v", err))
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Cache-Control"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// lastUserText returns the text of the last user message (a string, or the text parts of a content list).
func lastUserText(messages []map[string]any) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if role, _ := messages[i]["role"].(string); role != "user" {
			continue
		}
		switch c := messages[i]["content"].(type) {
		case string:
			return c
		case []any:
			var parts []string
			for _, p := range c {
				if m, ok := p.(map[string]any); ok {
					if t, _ := m["type"].(string); t == "text" {
						if text, ok := m["text"].(string); ok {
							parts = append(parts, text)
						}
					}
				}
			}
			return strings.Join(parts, "\n")
		}
		return ""
	}
	return ""
}

// injectContext appends text to the first message when it is a system message, else adds one at the start.
func injectContext(messages []map[string]any, text string) []map[string]any {
	if len(messages) > 0 {
		if role, _ := messages[0]["role"].(string); role == "system" {
			first := make(map[string]any, len(messages[0]))
			for k, v := range messages[0] {
				first[k] = v
			}
			switch c := first["content"].(type) {
			case string:
				if c == "" {
					first["content"] = text
				} else {
					first["content"] = c + "\n\n" + text
				}
			case []any:
				first["content"] = append(append([]any{}, c...), map[string]any{"type": "text", "text": text})
			default:
				first["content"] = text
			}
			return append([]map[string]any{first}, messages[1:]...)
		}
	}
	return append([]map[string]any{{"role": "system", "content": text}}, messages...)
}
