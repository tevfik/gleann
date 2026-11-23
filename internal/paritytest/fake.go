package paritytest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/gleann/internal/quality"
)

// Fake is an httptest server that speaks the subset of the embedding, chat
// and rerank APIs gleann calls, so a test can point a real client at it and
// afterwards check which endpoint was reached, with which body and key:
//
//   - POST /api/embed          Ollama batch embeddings ({input: string or
//     []string} → {embeddings}); vectors are quality.EmbedText.
//   - POST /api/embeddings     Ollama legacy single prompt ({prompt} →
//     {embedding}); not called by today's embedding.Computer, kept for
//     compatibility with the CLI harness fake.
//   - POST /v1/embeddings      OpenAI-compatible ({input} → {data:[{index,
//     embedding}], usage}).
//   - POST /api/chat           Ollama chat; stream:false answers one JSON
//     object, stream:true answers NDJSON chunks.
//   - POST /api/generate       Ollama generate (the summarizers' path);
//     stream:false one JSON object, stream:true NDJSON chunks.
//   - POST /v1/chat/completions  OpenAI-compatible chat; stream:false one
//     JSON object, stream:true SSE ending in "data: [DONE]".
//   - POST /v1/rerank          Jina / llama-server / Voyage shape ({query,
//     documents, top_n or top_k}). The response carries both "results"
//     (Jina, llama-server) and "data" (Voyage); every document is scored
//     and returned, sorted by score, as llama-server does.
//   - POST /v2/rerank          Cohere shape; response carries "results".
//   - GET/POST /api/tags, /api/show, /api/version, /api/pull  the model
//     directory endpoints the CLI harness fake also answers.
//
// Every request is counted per path and its last body and headers are
// recorded, including requests to unknown paths (answered 404). Chat
// answers are a fixed reply (SetReply); embedding and rerank answers are
// deterministic functions of the request, so two calls with the same input
// give the same vectors and scores.
//
// Fake is safe for concurrent use.
type Fake struct {
	*httptest.Server

	mu         sync.Mutex
	reply      string
	requests   map[string]int
	bodies     map[string]string
	headers    map[string]http.Header
	failEmbed  bool
	failChat   bool
	failRerank bool
}

// NewFake starts a Fake and registers its shutdown with t.Cleanup.
func NewFake(t *testing.T) *Fake {
	t.Helper()
	f := &Fake{
		reply:    "The answer is forty-two.",
		requests: map[string]int{},
		bodies:   map[string]string{},
		headers:  map[string]http.Header{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// SetReply sets the text every chat and generate endpoint answers with.
func (f *Fake) SetReply(s string) {
	f.mu.Lock()
	f.reply = s
	f.mu.Unlock()
}

// SetFailEmbed makes the embedding endpoints answer 500 until cleared.
func (f *Fake) SetFailEmbed(fail bool) {
	f.mu.Lock()
	f.failEmbed = fail
	f.mu.Unlock()
}

// SetFailChat makes the chat and generate endpoints answer 500 until cleared.
func (f *Fake) SetFailChat(fail bool) {
	f.mu.Lock()
	f.failChat = fail
	f.mu.Unlock()
}

// SetFailRerank makes the rerank endpoints answer 500 until cleared.
func (f *Fake) SetFailRerank(fail bool) {
	f.mu.Lock()
	f.failRerank = fail
	f.mu.Unlock()
}

// Count returns how many requests reached path (0 when none did).
func (f *Fake) Count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path]
}

// LastBody returns the body of the most recent request to path ("" when
// none reached it).
func (f *Fake) LastBody(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[path]
}

// LastHeader returns a copy of the headers of the most recent request to
// path (nil when none reached it), e.g. to check the Authorization key.
func (f *Fake) LastHeader(path string) http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.headers[path].Clone()
}

// Reset zeroes every counter and recorded request, keeping the reply and
// failure settings.
func (f *Fake) Reset() {
	f.mu.Lock()
	f.requests = map[string]int{}
	f.bodies = map[string]string{}
	f.headers = map[string]http.Header{}
	f.mu.Unlock()
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests[r.URL.Path]++
	f.bodies[r.URL.Path] = string(body)
	f.headers[r.URL.Path] = r.Header.Clone()
	reply, failEmbed, failChat, failRerank := f.reply, f.failEmbed, f.failChat, f.failRerank
	f.mu.Unlock()

	switch r.URL.Path {
	case "/api/embed", "/api/embeddings", "/v1/embeddings":
		if failEmbed {
			http.Error(w, "embedding backend down", http.StatusInternalServerError)
			return
		}
	case "/api/chat", "/api/generate", "/v1/chat/completions":
		if failChat {
			http.Error(w, "llm backend down", http.StatusInternalServerError)
			return
		}
	case "/v1/rerank", "/v2/rerank":
		if failRerank {
			http.Error(w, "rerank backend down", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/embed":
		var req struct {
			Input json.RawMessage `json:"input"`
		}
		_ = json.Unmarshal(body, &req)
		texts := inputTexts(req.Input)
		out := make([][]float32, len(texts))
		for i, s := range texts {
			out[i] = quality.EmbedText(s)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	case "/api/embeddings":
		var req struct {
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(body, &req)
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": quality.EmbedText(req.Prompt)})
	case "/v1/embeddings":
		var req struct {
			Input json.RawMessage `json:"input"`
		}
		_ = json.Unmarshal(body, &req)
		texts := inputTexts(req.Input)
		data := make([]map[string]any, len(texts))
		for i, s := range texts {
			data[i] = map[string]any{"index": i, "embedding": quality.EmbedText(s)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  data,
			"usage": map[string]int{"prompt_tokens": 0, "total_tokens": 0},
		})
	case "/api/chat":
		if streamRequested(body) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			rc := http.NewResponseController(w)
			for _, chunk := range replyChunks(reply) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"message": map[string]string{"role": "assistant", "content": chunk},
					"done":    false,
				})
				_ = rc.Flush()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]string{"role": "assistant", "content": ""},
				"done":    true,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"role": "assistant", "content": reply},
			"done":    true,
		})
	case "/api/generate":
		if streamRequested(body) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			rc := http.NewResponseController(w)
			for _, chunk := range replyChunks(reply) {
				_ = json.NewEncoder(w).Encode(map[string]any{"response": chunk, "done": false})
				_ = rc.Flush()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": "", "done": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"response": reply, "done": true})
	case "/v1/chat/completions":
		if streamRequested(body) {
			w.Header().Set("Content-Type", "text/event-stream")
			rc := http.NewResponseController(w)
			for _, chunk := range replyChunks(reply) {
				data, _ := json.Marshal(map[string]any{
					"choices": []any{map[string]any{
						"delta": map[string]string{"content": chunk},
						"index": 0,
					}},
				})
				fmt.Fprintf(w, "data: %s\n\n", data)
				_ = rc.Flush()
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": reply},
				"finish_reason": "stop",
				"index":         0,
			}},
		})
	case "/v1/rerank", "/v2/rerank":
		f.serveRerank(w, body, r.URL.Path == "/v1/rerank")
	case "/api/tags":
		var ms []map[string]any
		for _, m := range []string{(quality.HashEmbedder{}).ModelName(), "bge-m3:latest", "nomic-embed-text:latest", "llama3.2:latest"} {
			ms = append(ms, map[string]any{"name": m, "model": m, "size": 1 << 20})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": ms})
	case "/api/show":
		_ = json.NewEncoder(w).Encode(map[string]any{"model_info": map[string]any{"general.context_length": 8192}})
	case "/api/version":
		_ = json.NewEncoder(w).Encode(map[string]string{"version": "0.9.0"})
	case "/api/pull":
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	default:
		http.NotFound(w, r)
	}
}

// serveRerank scores documents by how many query words they contain (the
// CLI harness fake's scheme) and returns every document sorted by score
// descending, ties by input index. withData adds the Voyage-style "data"
// key next to the Jina/Cohere-style "results" key.
func (f *Fake) serveRerank(w http.ResponseWriter, body []byte, withData bool) {
	var req struct {
		Query     string   `json:"query"`
		Documents []string `json:"documents"`
	}
	_ = json.Unmarshal(body, &req)
	type item struct {
		Index int     `json:"index"`
		Score float64 `json:"relevance_score"`
	}
	words := strings.Fields(strings.ToLower(req.Query))
	res := make([]item, 0, len(req.Documents))
	for i, d := range req.Documents {
		s := 0.0
		lower := strings.ToLower(d)
		for _, qw := range words {
			if strings.Contains(lower, qw) {
				s++
			}
		}
		res = append(res, item{Index: i, Score: s})
	}
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].Score != res[j].Score {
			return res[i].Score > res[j].Score
		}
		return res[i].Index < res[j].Index
	})
	out := map[string]any{"results": res}
	if withData {
		out["data"] = res
	}
	_ = json.NewEncoder(w).Encode(out)
}

// inputTexts decodes an embedding "input" field that may be one string or
// a list of strings (both are valid on the Ollama and OpenAI endpoints).
func inputTexts(raw json.RawMessage) []string {
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}
	}
	return nil
}

// streamRequested reports whether the request body asks for a stream.
func streamRequested(body []byte) bool {
	var req struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &req)
	return req.Stream
}

// replyChunks splits reply into deterministic pieces of at most 8 runes
// whose concatenation is exactly reply, so a streamed answer reassembles
// to the same text as a non-streamed one.
func replyChunks(reply string) []string {
	const size = 8
	var chunks []string
	rs := []rune(reply)
	for len(rs) > 0 {
		n := min(size, len(rs))
		chunks = append(chunks, string(rs[:n]))
		rs = rs[n:]
	}
	return chunks
}
