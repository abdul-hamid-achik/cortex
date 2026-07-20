package adapters

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestEmbedVeclite builds a Veclite whose ollamaEmbed points at a test server,
// so the real HTTP path (request shape, status handling, response decoding) is
// exercised without a live Ollama.
func newTestEmbedVeclite(url string) *Veclite {
	v := NewVeclite()
	v.Configure(VecliteConfig{EmbedURL: url, EmbedModel: "test-model"})
	return v
}

func TestOllamaEmbedPostsModelAndPromptAndDecodesEmbedding(t *testing.T) {
	var gotMethod, gotContentType, gotModel, gotPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(body, &req)
		gotModel, gotPrompt = req.Model, req.Prompt
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"embedding":[0.1,0.2,0.3]}`))
	}))
	defer srv.Close()

	emb, err := newTestEmbedVeclite(srv.URL).ollamaEmbed(context.Background(), "where is the callback")
	if err != nil {
		t.Fatalf("ollamaEmbed: %v", err)
	}
	if len(emb) != 3 || emb[0] != 0.1 || emb[2] != 0.3 {
		t.Fatalf("embedding = %v, want [0.1 0.2 0.3]", emb)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %s, want application/json", gotContentType)
	}
	if gotModel != "test-model" || gotPrompt != "where is the callback" {
		t.Errorf("request body model/prompt = %q/%q", gotModel, gotPrompt)
	}
}

func TestOllamaEmbedSurfacesNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := newTestEmbedVeclite(srv.URL).ollamaEmbed(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("expected an HTTP 404 error, got: %v", err)
	}
}

func TestOllamaEmbedRejectsEmptyEmbedding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"embedding":[]}`))
	}))
	defer srv.Close()

	_, err := newTestEmbedVeclite(srv.URL).ollamaEmbed(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "empty embedding") {
		t.Fatalf("expected an empty-embedding error, got: %v", err)
	}
}
