package a2a

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postMessage(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/a2a/v1/message:send", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func mountServer(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	s.Mount(mux)
	return mux
}

func errorStatus(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("error body %q: %v", w.Body.String(), err)
	}
	return e.Error.Status
}

func TestSendMessage_MalformedJSON(t *testing.T) {
	w := postMessage(t, mountServer(testServer()), `{"message":`)
	if w.Code != http.StatusBadRequest || errorStatus(t, w) != "INVALID_ARGUMENT" {
		t.Fatalf("malformed body: %d %s", w.Code, w.Body.String())
	}
}

func TestSendMessage_FileOnlyPartReachesHandler(t *testing.T) {
	s := NewServer(DefaultAgentCard("v", ""))
	var got SkillContext
	s.RegisterSkill("describe-image", func(ctx SkillContext) (string, error) {
		got = ctx
		return "a cat", nil
	})
	body, _ := json.Marshal(SendMessageRequest{Message: Message{
		MessageID: "m1", Role: "ROLE_USER",
		Parts: []Part{
			{File: &FilePart{Name: "cat.png", MimeType: "image/png", Bytes: "aGVsbG8="}},
			{File: &FilePart{Name: "empty.png"}}, // no bytes: ignored
		},
	}})
	w := postMessage(t, mountServer(s), string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if len(got.Files) != 1 || got.Files[0].Name != "cat.png" || got.Query != "" {
		t.Fatalf("handler context = %+v", got)
	}
	var resp SendMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// With no keyword match and no ask-rag, the only registered skill runs.
	if resp.Task == nil || resp.Task.Status.State != TaskStateCompleted || resp.Task.Artifacts[0].Name != "describe-image-result" {
		t.Fatalf("response = %s", w.Body.String())
	}
}

func TestSendMessage_NoSkillsRegistered(t *testing.T) {
	s := NewServer(DefaultAgentCard("v", ""))
	w := postMessage(t, mountServer(s), `{"message":{"parts":[{"text":"hello"}]}}`)
	if w.Code != http.StatusBadRequest || errorStatus(t, w) != "UNSUPPORTED_OPERATION" {
		t.Fatalf("no skills: %d %s", w.Code, w.Body.String())
	}
}

func TestSendMessage_EvictsFinishedTasksPastLimit(t *testing.T) {
	s := testServer()
	mux := mountServer(s)
	body := []byte(`{"message":{"parts":[{"text":"search for x"}]}}`)
	for i := 0; i < 1001; i++ {
		req := httptest.NewRequest(http.MethodPost, "/a2a/v1/message:send", bytes.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	s.mu.RLock()
	n := len(s.tasks)
	s.mu.RUnlock()
	if n > 500 {
		t.Fatalf("%d tasks kept after passing the 1000-task limit, want <= 500", n)
	}
}

func TestRouteSkill_ScoringSkipsUnregisteredSkills(t *testing.T) {
	s := NewServer(DefaultAgentCard("v", ""))
	s.RegisterSkill("semantic-search", func(SkillContext) (string, error) { return "", nil })
	// "callgraphs" scores for code-analysis by prefix, but that skill is not
	// registered; "searching" gives semantic-search a score.
	if got := s.routeSkill("callgraphs searching", nil); got != "semantic-search" {
		t.Fatalf("routeSkill = %q", got)
	}
}

func TestDefaultAgentCard_DefaultsAndEnvOverride(t *testing.T) {
	t.Setenv("GLEANN_A2A_BASE_URL", "https://agents.example/gleann")
	card := DefaultAgentCard("", "")
	if card.Version != "dev" {
		t.Errorf("version = %q, want dev", card.Version)
	}
	if len(card.SupportedInterfaces) != 1 || !strings.HasPrefix(card.SupportedInterfaces[0].URL, "https://agents.example/gleann") {
		t.Errorf("interfaces = %+v, want the env base URL", card.SupportedInterfaces)
	}
}

func TestTaskFSMTransitions(t *testing.T) {
	t.Run("cancel from working", func(t *testing.T) {
		sm := NewTaskFSM(TaskStateSubmitted)
		if err := TaskFSMTransition(sm, TaskStateWorking); err != nil {
			t.Fatal(err)
		}
		if err := TaskFSMTransition(sm, TaskStateCanceled); err != nil {
			t.Fatalf("cancel: %v", err)
		}
		if err := TaskFSMTransition(sm, TaskStateWorking); err == nil {
			t.Fatal("a canceled task is terminal")
		}
	})
	t.Run("input required", func(t *testing.T) {
		sm := NewTaskFSM(TaskStateWorking)
		if err := TaskFSMTransition(sm, TaskStateInputRequired); err != nil {
			t.Fatalf("require input: %v", err)
		}
		// The follow-up message resumes the task (it used to fire "start",
		// which INPUT_REQUIRED refuses).
		if err := TaskFSMTransition(sm, TaskStateWorking); err != nil {
			t.Fatalf("resume: %v", err)
		}
		if err := TaskFSMTransition(sm, TaskStateCompleted); err != nil {
			t.Fatalf("complete after resume: %v", err)
		}
		if err := TaskFSMTransition(sm, TaskStateWorking); err == nil {
			t.Fatal("a completed task is terminal")
		}
	})
	t.Run("unknown target", func(t *testing.T) {
		sm := NewTaskFSM(TaskStateSubmitted)
		err := TaskFSMTransition(sm, TaskStateSubmitted)
		if err == nil || !strings.Contains(err.Error(), "no trigger for target state") {
			t.Fatalf("err = %v", err)
		}
	})
}
