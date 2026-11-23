package paritytest

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/quality"
	"github.com/tevfik/gleann/pkg/gleann"
)

// postJSON posts body to the fake's path and returns the decoded JSON
// response, failing the test on any transport or status error.
func postJSON(t *testing.T, f *Fake, path, body string) map[string]any {
	t.Helper()
	resp, err := http.Post(f.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status %d", path, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("POST %s: decode: %v", path, err)
	}
	return out
}

func vectorsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// jsonVector converts a decoded JSON number array back to float32.
func jsonVector(t *testing.T, v any) []float32 {
	t.Helper()
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("vector is %T, want []any", v)
	}
	out := make([]float32, len(arr))
	for i, x := range arr {
		f, ok := x.(float64)
		if !ok {
			t.Fatalf("vector element %d is %T", i, x)
		}
		out[i] = float32(f)
	}
	return out
}

func TestFakeEmbeddingEndpoints(t *testing.T) {
	f := NewFake(t)
	texts := []string{"HarvestApples(rows int)", "lighthouse keeper"}

	t.Run("ollama batch", func(t *testing.T) {
		body := `{"model":"m","input":["HarvestApples(rows int)","lighthouse keeper"],"truncate":true}`
		out := postJSON(t, f, "/api/embed", body)
		embs, ok := out["embeddings"].([]any)
		if !ok || len(embs) != len(texts) {
			t.Fatalf("embeddings = %v, want %d vectors", out["embeddings"], len(texts))
		}
		for i, text := range texts {
			if got := jsonVector(t, embs[i]); !vectorsEqual(got, quality.EmbedText(text)) {
				t.Errorf("vector %d differs from quality.EmbedText", i)
			}
		}
		if f.Count("/api/embed") != 1 {
			t.Errorf("count = %d, want 1", f.Count("/api/embed"))
		}
		if f.LastBody("/api/embed") != body {
			t.Errorf("last body not recorded: %q", f.LastBody("/api/embed"))
		}
	})

	t.Run("ollama single string input", func(t *testing.T) {
		out := postJSON(t, f, "/api/embed", `{"model":"m","input":"lighthouse keeper"}`)
		embs := out["embeddings"].([]any)
		if len(embs) != 1 || !vectorsEqual(jsonVector(t, embs[0]), quality.EmbedText(texts[1])) {
			t.Fatalf("single-string input: %v", out)
		}
	})

	t.Run("ollama legacy prompt", func(t *testing.T) {
		out := postJSON(t, f, "/api/embeddings", `{"model":"m","prompt":"lighthouse keeper"}`)
		if got := jsonVector(t, out["embedding"]); !vectorsEqual(got, quality.EmbedText(texts[1])) {
			t.Error("legacy endpoint vector differs from quality.EmbedText")
		}
		if f.Count("/api/embeddings") != 1 {
			t.Errorf("count = %d, want 1", f.Count("/api/embeddings"))
		}
	})

	t.Run("openai", func(t *testing.T) {
		out := postJSON(t, f, "/v1/embeddings", `{"model":"m","input":["HarvestApples(rows int)","lighthouse keeper"]}`)
		data, ok := out["data"].([]any)
		if !ok || len(data) != len(texts) {
			t.Fatalf("data = %v, want %d entries", out["data"], len(texts))
		}
		for i, text := range texts {
			entry := data[i].(map[string]any)
			if entry["index"] != float64(i) {
				t.Errorf("entry %d: index = %v", i, entry["index"])
			}
			if got := jsonVector(t, entry["embedding"]); !vectorsEqual(got, quality.EmbedText(text)) {
				t.Errorf("entry %d: vector differs from quality.EmbedText", i)
			}
		}
		if out["usage"] == nil {
			t.Error("usage missing")
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		body := `{"input":["same text twice"]}`
		a := postJSON(t, f, "/api/embed", body)["embeddings"].([]any)
		b := postJSON(t, f, "/api/embed", body)["embeddings"].([]any)
		if !vectorsEqual(jsonVector(t, a[0]), jsonVector(t, b[0])) {
			t.Error("two calls with the same input gave different vectors")
		}
	})
}

func TestFakeEmbeddingsViaRealComputer(t *testing.T) {
	f := NewFake(t)
	ctx := context.Background()
	texts := []string{"PlantAppleTree", "tugboats at dawn"}

	ollama := embedding.NewComputer(embedding.Options{
		Provider: embedding.ProviderOllama, Model: "test-hashed-bow-256", BaseURL: f.URL,
	})
	got, err := ollama.Compute(ctx, texts)
	if err != nil {
		t.Fatalf("ollama Compute: %v", err)
	}
	for i, text := range texts {
		if !vectorsEqual(got[i], quality.EmbedText(text)) {
			t.Errorf("ollama vector %d differs from quality.EmbedText", i)
		}
	}
	if f.Count("/api/embed") != 1 {
		t.Errorf("/api/embed count = %d, want 1", f.Count("/api/embed"))
	}

	openai := embedding.NewComputer(embedding.Options{
		Provider: embedding.ProviderOpenAI, Model: "m", BaseURL: f.URL, APIKey: "sk-test",
	})
	got, err = openai.Compute(ctx, texts)
	if err != nil {
		t.Fatalf("openai Compute: %v", err)
	}
	for i, text := range texts {
		if !vectorsEqual(got[i], quality.EmbedText(text)) {
			t.Errorf("openai vector %d differs from quality.EmbedText", i)
		}
	}
	if f.Count("/v1/embeddings") != 1 {
		t.Errorf("/v1/embeddings count = %d, want 1", f.Count("/v1/embeddings"))
	}
	if auth := f.LastHeader("/v1/embeddings").Get("Authorization"); auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want Bearer sk-test", auth)
	}
}

func TestFakeChatNonStreaming(t *testing.T) {
	f := NewFake(t)
	ctx := context.Background()
	msgs := []gleann.ChatMessage{{Role: "user", Content: "what is the answer?"}}

	ollama := gleann.NewChat(nil, gleann.ChatConfig{Provider: gleann.LLMOllama, Model: "m", BaseURL: f.URL})
	got, err := ollama.Complete(ctx, msgs)
	if err != nil {
		t.Fatalf("ollama Complete: %v", err)
	}
	if got != "The answer is forty-two." {
		t.Errorf("ollama reply = %q", got)
	}
	if f.Count("/api/chat") != 1 {
		t.Errorf("/api/chat count = %d, want 1", f.Count("/api/chat"))
	}
	if !strings.Contains(f.LastBody("/api/chat"), "what is the answer?") {
		t.Errorf("last body missing the question: %q", f.LastBody("/api/chat"))
	}

	openai := gleann.NewChat(nil, gleann.ChatConfig{Provider: gleann.LLMOpenAI, Model: "m", BaseURL: f.URL, APIKey: "sk"})
	got, err = openai.Complete(ctx, msgs)
	if err != nil {
		t.Fatalf("openai Complete: %v", err)
	}
	if got != "The answer is forty-two." {
		t.Errorf("openai reply = %q", got)
	}
	if f.Count("/v1/chat/completions") != 1 {
		t.Errorf("/v1/chat/completions count = %d, want 1", f.Count("/v1/chat/completions"))
	}

	out := postJSON(t, f, "/api/generate", `{"model":"m","prompt":"summarize","stream":false}`)
	if out["response"] != "The answer is forty-two." || out["done"] != true {
		t.Errorf("generate response = %v", out)
	}

	f.SetReply("a different answer")
	got, err = ollama.Complete(ctx, msgs)
	if err != nil || got != "a different answer" {
		t.Errorf("after SetReply: %q, %v", got, err)
	}
}

// streamNDJSON reads an Ollama NDJSON stream and returns the concatenated
// content, checking that the final line is marked done.
func streamNDJSON(t *testing.T, resp *http.Response, field string) string {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d", resp.StatusCode)
	}
	var sb strings.Builder
	done := false
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal(line, &chunk); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", line, err)
		}
		if field == "message" {
			if msg, ok := chunk["message"].(map[string]any); ok {
				sb.WriteString(msg["content"].(string))
			}
		} else {
			sb.WriteString(chunk[field].(string))
		}
		if chunk["done"] == true {
			done = true
			break
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !done {
		t.Error("stream ended without done:true")
	}
	return sb.String()
}

// streamSSE reads an OpenAI SSE stream and returns the concatenated delta
// content, checking the [DONE] terminator.
func streamSSE(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d", resp.StatusCode)
	}
	var sb strings.Builder
	terminated := false
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			terminated = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("bad SSE data %q: %v", data, err)
		}
		for _, c := range chunk.Choices {
			sb.WriteString(c.Delta.Content)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !terminated {
		t.Error("SSE stream ended without data: [DONE]")
	}
	return sb.String()
}

func TestFakeChatStreaming(t *testing.T) {
	f := NewFake(t)
	f.SetReply("The answer is forty-two, streamed in small pieces.")

	resp, err := http.Post(f.URL+"/api/chat", "application/json",
		strings.NewReader(`{"model":"m","messages":[],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := streamNDJSON(t, resp, "message"); got != "The answer is forty-two, streamed in small pieces." {
		t.Errorf("ollama stream reassembled to %q", got)
	}

	resp, err = http.Post(f.URL+"/api/generate", "application/json",
		strings.NewReader(`{"model":"m","prompt":"p","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := streamNDJSON(t, resp, "response"); got != "The answer is forty-two, streamed in small pieces." {
		t.Errorf("generate stream reassembled to %q", got)
	}

	resp, err = http.Post(f.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"m","messages":[],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := streamSSE(t, resp); got != "The answer is forty-two, streamed in small pieces." {
		t.Errorf("openai stream reassembled to %q", got)
	}

	if f.Count("/api/chat") != 1 || f.Count("/api/generate") != 1 || f.Count("/v1/chat/completions") != 1 {
		t.Errorf("counts: chat=%d generate=%d completions=%d",
			f.Count("/api/chat"), f.Count("/api/generate"), f.Count("/v1/chat/completions"))
	}
}

func TestFakeRerank(t *testing.T) {
	f := NewFake(t)
	ctx := context.Background()
	docs := []string{
		"completely unrelated text",
		"apples and more apples",
		"apples harvested once",
	}
	results := []gleann.SearchResult{
		{ID: 10, Text: docs[0]},
		{ID: 11, Text: docs[1]},
		{ID: 12, Text: docs[2]},
	}

	t.Run("raw v1", func(t *testing.T) {
		out := postJSON(t, f, "/v1/rerank",
			`{"model":"m","query":"apples harvested","top_n":3,"documents":["completely unrelated text","apples and more apples","apples harvested once"]}`)
		for _, key := range []string{"results", "data"} {
			arr, ok := out[key].([]any)
			if !ok || len(arr) != 3 {
				t.Fatalf("%s = %v, want 3 entries", key, out[key])
			}
			first := arr[0].(map[string]any)
			if first["index"] != float64(2) || first["relevance_score"] != float64(2) {
				t.Errorf("%s[0] = %v, want index 2 score 2", key, first)
			}
			last := arr[2].(map[string]any)
			if last["index"] != float64(0) || last["relevance_score"] != float64(0) {
				t.Errorf("%s[2] = %v, want index 0 score 0", key, last)
			}
		}
	})

	t.Run("raw v2 cohere shape", func(t *testing.T) {
		out := postJSON(t, f, "/v2/rerank",
			`{"model":"m","query":"apples harvested","top_n":3,"documents":["completely unrelated text","apples and more apples","apples harvested once"]}`)
		if out["results"] == nil {
			t.Fatal("results missing")
		}
		if out["data"] != nil {
			t.Error("v2/rerank should carry only results")
		}
	})

	t.Run("llamacpp provider", func(t *testing.T) {
		r := gleann.NewReranker(gleann.RerankerConfig{Provider: gleann.RerankerLlamacpp, Model: "m", BaseURL: f.URL})
		got, err := r.Rerank(ctx, "apples harvested", results, 0)
		if err != nil {
			t.Fatalf("Rerank: %v", err)
		}
		if len(got) != 3 || got[0].ID != 12 || got[0].Score != 2 {
			t.Fatalf("reranked = %+v, want ID 12 first with score 2", got)
		}
		if f.Count("/v1/rerank") != 2 { // raw v1 + this call
			t.Errorf("/v1/rerank count = %d, want 2", f.Count("/v1/rerank"))
		}
		if !strings.Contains(f.LastBody("/v1/rerank"), "apples and more apples") {
			t.Errorf("last body missing documents: %q", f.LastBody("/v1/rerank"))
		}
	})

	t.Run("cohere provider", func(t *testing.T) {
		r := gleann.NewReranker(gleann.RerankerConfig{Provider: gleann.RerankerCohere, Model: "m", BaseURL: f.URL, APIKey: "ck"})
		got, err := r.Rerank(ctx, "apples harvested", results, 0)
		if err != nil {
			t.Fatalf("Rerank: %v", err)
		}
		if len(got) != 3 || got[0].ID != 12 {
			t.Fatalf("reranked = %+v", got)
		}
		if f.Count("/v2/rerank") != 2 { // raw v2 + this call
			t.Errorf("/v2/rerank count = %d, want 2", f.Count("/v2/rerank"))
		}
		if auth := f.LastHeader("/v2/rerank").Get("Authorization"); auth != "Bearer ck" {
			t.Errorf("Authorization = %q, want Bearer ck", auth)
		}
	})

	t.Run("voyage provider", func(t *testing.T) {
		r := gleann.NewReranker(gleann.RerankerConfig{Provider: gleann.RerankerVoyage, Model: "m", BaseURL: f.URL, APIKey: "vk"})
		got, err := r.Rerank(ctx, "apples harvested", results, 0)
		if err != nil {
			t.Fatalf("Rerank: %v", err)
		}
		if len(got) != 3 || got[0].ID != 12 {
			t.Fatalf("reranked = %+v", got)
		}
		if f.Count("/v1/rerank") != 3 {
			t.Errorf("/v1/rerank count = %d, want 3", f.Count("/v1/rerank"))
		}
	})
}

func TestFakeCountersAndRecording(t *testing.T) {
	f := NewFake(t)

	postJSON(t, f, "/api/embed", `{"input":["a"]}`)
	postJSON(t, f, "/api/embed", `{"input":["b"]}`)
	postJSON(t, f, "/api/chat", `{"messages":[]}`)

	if f.Count("/api/embed") != 2 || f.Count("/api/chat") != 1 || f.Count("/v1/rerank") != 0 {
		t.Errorf("counts: embed=%d chat=%d rerank=%d", f.Count("/api/embed"), f.Count("/api/chat"), f.Count("/v1/rerank"))
	}
	if f.LastBody("/api/embed") != `{"input":["b"]}` {
		t.Errorf("last body = %q, want the second request", f.LastBody("/api/embed"))
	}
	if f.LastBody("/v1/rerank") != "" || f.LastHeader("/v1/rerank") != nil {
		t.Error("unreached endpoint should have no record")
	}

	req, err := http.NewRequest(http.MethodPost, f.URL+"/api/embed", strings.NewReader(`{"input":["c"]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer rotated-key")
	req.Header.Set("X-Custom", "v")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	hdr := f.LastHeader("/api/embed")
	if hdr.Get("Authorization") != "Bearer rotated-key" || hdr.Get("X-Custom") != "v" {
		t.Errorf("headers not recorded: %v", hdr)
	}
	hdr.Set("Authorization", "mutated")
	if f.LastHeader("/api/embed").Get("Authorization") != "Bearer rotated-key" {
		t.Error("LastHeader must return a copy")
	}

	// Unknown paths are counted and answered 404.
	resp, err = http.Post(f.URL+"/no/such/path", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown path status = %d, want 404", resp.StatusCode)
	}
	if f.Count("/no/such/path") != 1 {
		t.Errorf("unknown path count = %d, want 1", f.Count("/no/such/path"))
	}

	f.Reset()
	if f.Count("/api/embed") != 0 || f.LastBody("/api/embed") != "" || f.LastHeader("/api/embed") != nil {
		t.Error("Reset did not clear the records")
	}
	postJSON(t, f, "/api/chat", `{"messages":[]}`)
	if f.Count("/api/chat") != 1 {
		t.Error("counting after Reset failed")
	}
}

func TestFakeFailures(t *testing.T) {
	f := NewFake(t)

	f.SetFailEmbed(true)
	f.SetFailChat(true)
	f.SetFailRerank(true)
	for _, path := range []string{"/api/embed", "/api/embeddings", "/v1/embeddings", "/api/chat", "/api/generate", "/v1/chat/completions", "/v1/rerank", "/v2/rerank"} {
		resp, err := http.Post(f.URL+path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("%s status = %d, want 500", path, resp.StatusCode)
		}
	}

	f.SetFailEmbed(false)
	f.SetFailChat(false)
	f.SetFailRerank(false)
	postJSON(t, f, "/api/embed", `{"input":["a"]}`)
	postJSON(t, f, "/api/chat", `{"messages":[]}`)
	postJSON(t, f, "/v1/rerank", `{"query":"q","documents":["d"]}`)
}

func TestFakeModelDirectory(t *testing.T) {
	f := NewFake(t)
	out := postJSON(t, f, "/api/tags", `{}`)
	models, ok := out["models"].([]any)
	if !ok || len(models) == 0 {
		t.Fatalf("models = %v", out["models"])
	}
	found := false
	for _, m := range models {
		if m.(map[string]any)["name"] == (quality.HashEmbedder{}).ModelName() {
			found = true
		}
	}
	if !found {
		t.Errorf("tags missing the hash model: %v", models)
	}
	if out := postJSON(t, f, "/api/version", `{}`); out["version"] == nil {
		t.Error("version missing")
	}
	if out := postJSON(t, f, "/api/show", `{}`); out["model_info"] == nil {
		t.Error("model_info missing")
	}
}
