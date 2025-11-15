package server

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"home-podcast/internal/models"
)

type fakeLibrary struct {
	episodes []models.Episode
}

func (f *fakeLibrary) ListEpisodes() []models.Episode {
	return f.episodes
}

func testFeedMetadata() FeedMetadata {
	return FeedMetadata{
		Title:       "Test Feed",
		Description: "Test feed description",
		Language:    "en",
		Author:      "Test Author",
	}
}

func TestHealthEndpoint(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("unexpected status payload: %v", body)
	}
}

func TestHealthEndpointRejectsNonGET(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestEpisodesEndpoint(t *testing.T) {
	episodes := []models.Episode{
		{
			ID:            "ep1",
			Filename:      "ep1.mp3",
			RelativePath:  "ep1.mp3",
			Title:         "Episode 1",
			FilesizeBytes: 123,
			ModifiedAt:    time.Unix(0, 0).UTC(),
		},
	}
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{episodes: episodes}, nil, audioDir, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/episodes", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload []models.Episode
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(payload) != 1 || payload[0].ID != "ep1" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestEpisodesEndpointRejectsNonGET(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodDelete, "/episodes", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

type fakeValidator struct {
	allowed map[string]struct{}
}

func (f *fakeValidator) IsValidToken(token string) bool {
	_, ok := f.allowed[token]
	return ok
}

func TestEpisodesEndpointRequiresToken(t *testing.T) {
	validator := &fakeValidator{allowed: map[string]struct{}{"secret": {}}}
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, validator, audioDir, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/episodes", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/episodes?token=secret", nil)
	rec = httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", rec.Code)
	}
}

func TestFeedEndpointProducesRSS(t *testing.T) {
	audioDir := t.TempDir()
	episodes := []models.Episode{
		{
			ID:            "episode-1",
			Filename:      "episode-1.mp3",
			RelativePath:  "episode-1.mp3",
			Title:         "Episode 1",
			FilesizeBytes: 2048,
			ModifiedAt:    time.Unix(1700000000, 0).UTC(),
			DurationSeconds: func() *float64 {
				value := 321.0
				return &value
			}(),
			Artist: func() *string {
				value := "Test Artist"
				return &value
			}(),
		},
	}

	handler := New(&fakeLibrary{episodes: episodes}, nil, audioDir, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/feed", nil)
	req.Host = "feed.example"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/rss+xml") {
		t.Fatalf("unexpected content type %q", ct)
	}

	var payload struct {
		Channel struct {
			Title string `xml:"title"`
			Items []struct {
				Title     string `xml:"title"`
				Enclosure struct {
					URL string `xml:"url,attr"`
				} `xml:"enclosure"`
				ITunesDuration string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
			} `xml:"item"`
		} `xml:"channel"`
	}

	if err := xml.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal rss: %v", err)
	}

	if payload.Channel.Title != "Test Feed" {
		t.Fatalf("unexpected channel title: %s", payload.Channel.Title)
	}

	if len(payload.Channel.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(payload.Channel.Items))
	}

	item := payload.Channel.Items[0]
	if item.Enclosure.URL != "https://feed.example/audio/episode-1.mp3" {
		t.Fatalf("unexpected enclosure URL: %s", item.Enclosure.URL)
	}
	if item.ITunesDuration == "" {
		t.Fatalf("expected itunes duration field")
	}
}

func TestFeedEndpointRequiresToken(t *testing.T) {
	validator := &fakeValidator{allowed: map[string]struct{}{"secret": {}}}
	audioDir := t.TempDir()
	episodes := []models.Episode{
		{
			ID:            "episode-1",
			Filename:      "episode-1.mp3",
			RelativePath:  "episode-1.mp3",
			Title:         "Episode 1",
			FilesizeBytes: 1234,
			ModifiedAt:    time.Unix(1700000000, 0).UTC(),
		},
	}
	handler := New(&fakeLibrary{episodes: episodes}, validator, audioDir, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/feed", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/feed?token=secret", nil)
	req.Host = "feed.example"
	rec = httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", rec.Code)
	}

	var payload struct {
		Channel struct {
			Items []struct {
				Enclosure struct {
					URL string `xml:"url,attr"`
				} `xml:"enclosure"`
			} `xml:"item"`
		} `xml:"channel"`
	}

	if err := xml.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal rss: %v", err)
	}

	if len(payload.Channel.Items) == 0 {
		t.Fatalf("expected at least one item")
	}

	encURL := payload.Channel.Items[0].Enclosure.URL
	if !strings.HasPrefix(encURL, "https://") {
		t.Fatalf("expected https enclosure URL, got %s", encURL)
	}
	if !strings.Contains(encURL, "token=secret") {
		t.Fatalf("expected token query in enclosure URL, got %s", encURL)
	}
}

func TestAudioEndpointServesFile(t *testing.T) {
	audioDir := t.TempDir()
	filePath := filepath.Join(audioDir, "clip.mp3")
	if err := os.WriteFile(filePath, []byte("audio-bytes"), 0o644); err != nil {
		t.Fatalf("write audio file: %v", err)
	}

	handler := New(&fakeLibrary{}, nil, audioDir, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/audio/clip.mp3", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "audio-bytes" {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestAudioEndpointRequiresToken(t *testing.T) {
	audioDir := t.TempDir()
	filePath := filepath.Join(audioDir, "clip.mp3")
	if err := os.WriteFile(filePath, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write audio file: %v", err)
	}

	validator := &fakeValidator{allowed: map[string]struct{}{"secret": {}}}
	handler := New(&fakeLibrary{}, validator, audioDir, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/audio/clip.mp3", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/audio/clip.mp3?token=secret", nil)
	rec = httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", rec.Code)
	}
}

func TestAudioEndpointPreventsTraversal(t *testing.T) {
	audioDir := t.TempDir()
	absDir, err := filepath.Abs(audioDir)
	if err != nil {
		t.Fatalf("abs audio dir: %v", err)
	}

	h := &serverHandler{
		audioRoot: absDir,
		logger:    log.New(io.Discard, "", 0),
	}

	req := httptest.NewRequest(http.MethodGet, "/audio/../secret.txt", nil)
	rec := httptest.NewRecorder()

	h.handleAudio(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for traversal attempt, got %d", rec.Code)
	}
}
