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
// of {"index", "source", "score"}. With several indexes, one that cannot be loaded or searched (for example an
// index in the old store format) is left out, logged, and listed in X-Gleann-Skipped-Indexes, a percent-encoded
// JSON list of {"index", "reason"}. The passages follow an intro sentence (proxy_context_intro, default English)
// that always ends with the instruction to answer in the language of the question. Optional headers:
//   - X-Gleann-Context: user puts the passages into the last user message (before its text) instead of the first
//     system message, so the conversation before it stays the same from turn to turn and the LLM server can reuse
//     its prompt cache for it;
//   - X-Gleann-Max-Context-Chars: N keeps the best passages up to N characters (at least one);
//   - X-Gleann-Hybrid-Alpha: A (0 to 1) weighs vector against lexical search for this request, 0 = lexical only;
//   - X-Gleann-Cover: true (several indexes) keeps the best passage of every index whose best passage is close to
//     the overall best, replacing the lowest-ranked extra passages of the others (proxy_cover, X-Gleann-Cover-Margin,
//     -Share and -Floor set what "close" means; see proxy_cover.go);
//   - X-Gleann-Rerank: true (the configured reranker) or a reranker model, X-Gleann-Rerank-Pool: N candidates.
//
// Example: X-Gleann-Index: project-docs,project-code; X-Gleann-Top-K: 8; X-Gleann-Context: user;
// X-Gleann-Max-Context-Chars: 8000.

// defaultPassthroughIntro opens the context the proxy adds to the conversation (proxy_context_intro or
// GLEANN_PROXY_CONTEXT_INTRO replace it).
const defaultPassthroughIntro = "Use the following context, retrieved from the user's documents, to answer. " +
	"If it does not contain the answer, say so."

// passthroughLanguageRule ends every intro, whatever its text: the answer follows the question's language, so no
// language needs to be detected or written into the intro.
const passthroughLanguageRule = "Answer in the language of the user's question."

// passthroughIntro returns the full text that precedes the retrieved passages: the intro (the environment
// variable, else the config, else the default), the language rule, and the "Context:" label.
func (s *Server) passthroughIntro() string {
	intro := strings.TrimSpace(os.Getenv("GLEANN_PROXY_CONTEXT_INTRO"))
	if intro == "" {
		intro = strings.TrimSpace(s.cfg().ProxyContextIntro)
	}
	if intro == "" {
		intro = defaultPassthroughIntro
	}
	return intro + " " + passthroughLanguageRule + "\n\nContext:\n"
}

// maxPassthroughBody bounds a proxied request body.
const maxPassthroughBody = 64 << 20

func (s *Server) proxyPassthrough() bool {
	if v := os.Getenv("GLEANN_PROXY_MODE"); v != "" {
		return strings.EqualFold(v, "passthrough")
	}
	return strings.EqualFold(s.cfg().ProxyMode, "passthrough")
}

// passthroughUpstream returns the LLM server's base URL (without /v1) and API
// key. For the openai provider without a configured endpoint it falls back to
// OPENAI_BASE_URL only and otherwise fails: OpenAI's cloud is never a default.
func (s *Server) passthroughUpstream() (string, string, error) {
	cfg := s.proxyLLMConfig()
	if cfg.Provider == gleann.LLMOpenAI && cfg.BaseURL == "" {
		cfg.BaseURL = gleann.ResolveOpenAIBaseURL("")
		if cfg.BaseURL == "" {
			return "", "", gleann.ErrOpenAIEndpointUnset
		}
	}
	if cfg.BaseURL == "" {
		return "", "", fmt.Errorf("proxy_mode passthrough: no endpoint for provider %q", cfg.Provider)
	}
	return gleann.NormalizeOpenAIBaseURL(cfg.BaseURL), cfg.APIKey, nil
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
		if o, ok := hybridAlphaOption(r); ok {
			searchOpts = append(searchOpts, o)
		}
		if v := strings.TrimSpace(r.Header.Get("X-Gleann-Rerank")); v != "" && !strings.EqualFold(v, "false") && v != "0" {
			model := ""
			if !strings.EqualFold(v, "true") && v != "1" {
				model = v
			}
			searchOpts = append(searchOpts, gleann.WithCustomReranker(gleann.NewReranker(gleann.ResolveRerankerConfig(s.cfg(), model))))
			if n, err := strconv.Atoi(r.Header.Get("X-Gleann-Rerank-Pool")); err == nil && n > 0 {
				searchOpts = append(searchOpts, gleann.WithRerankPool(n))
			}
		}
		maxChars, _ := strconv.Atoi(r.Header.Get("X-Gleann-Max-Context-Chars"))
		block, sources, skipped, err := s.retrieveProxyContextLimited(r.Context(), lastUserText(messages), indexNames,
			searchOpts, maxChars, coverFromRequest(s.cfg(), r))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "RAG retrieval failed: "+err.Error())
			return
		}
		if len(skipped) > 0 {
			if list, err := json.Marshal(skipped); err == nil {
				w.Header().Set("X-Gleann-Skipped-Indexes", url.QueryEscape(string(list)))
			}
		}
		if block != "" {
			intro := s.passthroughIntro()
			if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Gleann-Context")), "user") {
				messages = injectContextUser(messages, intro+block)
			} else {
				messages = injectContext(messages, intro+block)
			}
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

	base, apiKey, err := s.passthroughUpstream()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	s.forwardPassthrough(w, r, base+"/v1/chat/completions", out, apiKey)
}

// handleProxyStop forwards POST /stop to the LLM server (pass-through mode only).
func (s *Server) handleProxyStop(w http.ResponseWriter, r *http.Request) {
	if !s.proxyPassthrough() {
		writeError(w, http.StatusNotFound, "POST /stop is forwarded in proxy_mode passthrough only")
		return
	}
	base, apiKey, err := s.passthroughUpstream()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
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

// injectContextUser puts text before the content of the last user message, followed by "Question:" and the
// message's own text (a content list gets text as its first part). The messages before it stay unchanged.
func injectContextUser(messages []map[string]any, text string) []map[string]any {
	for i := len(messages) - 1; i >= 0; i-- {
		if role, _ := messages[i]["role"].(string); role != "user" {
			continue
		}
		msg := make(map[string]any, len(messages[i]))
		for k, v := range messages[i] {
			msg[k] = v
		}
		switch c := msg["content"].(type) {
		case string:
			msg["content"] = text + "\n\nQuestion:\n" + c
		case []any:
			msg["content"] = append([]any{map[string]any{"type": "text", "text": text + "\n\nQuestion:"}}, c...)
		default:
			msg["content"] = text
		}
		out := append([]map[string]any{}, messages...)
		out[i] = msg
		return out
	}
	return injectContext(messages, text)
}
