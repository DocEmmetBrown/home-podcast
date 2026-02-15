package server

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"io"
	"log"
	"mime/multipart"
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
	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

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
	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

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
	handler := New(&fakeLibrary{episodes: episodes}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

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
	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

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
	handler := New(&fakeLibrary{}, validator, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

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

	handler := New(&fakeLibrary{episodes: episodes}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

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
	handler := New(&fakeLibrary{episodes: episodes}, validator, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

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

	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

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
	handler := New(&fakeLibrary{}, validator, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

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

func TestExtractToken(t *testing.T) {
	tests := []struct {
		name  string
		setup func(r *http.Request)
		want  string
	}{
		{
			name:  "query param",
			setup: func(r *http.Request) {},
			want:  "qtoken",
		},
		{
			name: "X-Podcast-Token header",
			setup: func(r *http.Request) {
				r.Header.Set("X-Podcast-Token", "htoken")
			},
			want: "htoken",
		},
		{
			name: "Authorization Bearer header",
			setup: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer btoken")
			},
			want: "btoken",
		},
		{
			name: "cookie with non-bearer auth header",
			setup: func(r *http.Request) {
				r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
				r.AddCookie(&http.Cookie{Name: authCookieName, Value: "ctoken"})
			},
			want: "ctoken",
		},
		{
			name:  "no token",
			setup: func(r *http.Request) {},
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url := "/episodes"
			if tc.name == "query param" {
				url = "/episodes?token=qtoken"
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			tc.setup(req)
			got := extractToken(req)
			if got != tc.want {
				t.Fatalf("extractToken() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractTokenPrecedence(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/episodes?token=query", nil)
	req.Header.Set("X-Podcast-Token", "header")
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: "cookie"})

	got := extractToken(req)
	if got != "query" {
		t.Fatalf("expected query param to take precedence, got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/episodes", nil)
	req.Header.Set("X-Podcast-Token", "header")
	req.Header.Set("Authorization", "Bearer bearer")

	got = extractToken(req)
	if got != "header" {
		t.Fatalf("expected X-Podcast-Token to take precedence over Bearer, got %q", got)
	}
}

func TestIsHTTPSRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if isHTTPSRequest(req) {
		t.Fatalf("expected false for plain HTTP")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.TLS = &tls.ConnectionState{}
	if !isHTTPSRequest(req) {
		t.Fatalf("expected true when TLS is set")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if !isHTTPSRequest(req) {
		t.Fatalf("expected true with X-Forwarded-Proto: https")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	if isHTTPSRequest(req) {
		t.Fatalf("expected false with X-Forwarded-Proto: http")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Proto", "HTTPS, http")
	if !isHTTPSRequest(req) {
		t.Fatalf("expected true with first proto HTTPS in comma list")
	}
}

func TestPathWithinRoot(t *testing.T) {
	if !pathWithinRoot("/srv/audio", "/srv/audio/file.mp3") {
		t.Fatalf("expected child path to be within root")
	}
	if !pathWithinRoot("/srv/audio", "/srv/audio/sub/file.mp3") {
		t.Fatalf("expected nested child to be within root")
	}
	if pathWithinRoot("/srv/audio", "/srv/other/file.mp3") {
		t.Fatalf("expected unrelated path to be outside root")
	}
	if pathWithinRoot("/srv/audio", "/srv/audio/../other/file.mp3") {
		t.Fatalf("expected traversal path to be outside root")
	}
	if !pathWithinRoot("/srv/audio", "/srv/audio") {
		t.Fatalf("expected root itself to be within root")
	}
}

func TestMimeTypeForFilename(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"song.mp3", "audio/mpeg"},
		{"song.m4a", "audio/mp4"},
		{"song.aac", "audio/aac"},
		{"song.flac", "audio/flac"},
		{"song.ogg", "audio/ogg"},
		{"noext", "application/octet-stream"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mimeTypeForFilename(tc.name)
			if got != tc.want {
				t.Fatalf("mimeTypeForFilename(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestRequestBaseURL(t *testing.T) {
	h := &serverHandler{logger: log.New(io.Discard, "", 0)}

	req := httptest.NewRequest(http.MethodGet, "/feed", nil)
	req.Host = "example.com"
	u := h.requestBaseURL(req)
	if u == nil || u.Scheme != "http" || u.Host != "example.com" {
		t.Fatalf("unexpected base URL: %v", u)
	}

	req = httptest.NewRequest(http.MethodGet, "/feed", nil)
	req.Host = "example.com"
	req.TLS = &tls.ConnectionState{}
	u = h.requestBaseURL(req)
	if u == nil || u.Scheme != "https" {
		t.Fatalf("expected https with TLS, got %v", u)
	}

	req = httptest.NewRequest(http.MethodGet, "/feed", nil)
	req.Host = "example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	u = h.requestBaseURL(req)
	if u == nil || u.Scheme != "https" {
		t.Fatalf("expected https with X-Forwarded-Proto, got %v", u)
	}

	req = httptest.NewRequest(http.MethodGet, "/feed", nil)
	req.Host = ""
	u = h.requestBaseURL(req)
	if u != nil {
		t.Fatalf("expected nil for empty host, got %v", u)
	}
}

func TestHandleUI(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("expected no-store cache control, got %q", cc)
	}
}

func TestHandleUIRejectsNonGET(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodPost, "/ui", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleUIRequiresToken(t *testing.T) {
	validator := &fakeValidator{allowed: map[string]struct{}{"secret": {}}}
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, validator, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/ui?token=secret", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", rec.Code)
	}
}

func TestHandleUISetsAuthCookie(t *testing.T) {
	validator := &fakeValidator{allowed: map[string]struct{}{"secret": {}}}
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, validator, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/ui?token=secret", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	cookies := rec.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == authCookieName {
			found = true
			if c.Value != "secret" {
				t.Fatalf("expected cookie value 'secret', got %q", c.Value)
			}
			if !c.HttpOnly {
				t.Fatalf("expected HttpOnly cookie")
			}
		}
	}
	if !found {
		t.Fatalf("expected auth cookie to be set")
	}
}

func TestSetAuthCookieEmptyToken(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	setAuthCookie(rec, req, "")

	if len(rec.Result().Cookies()) != 0 {
		t.Fatalf("expected no cookie for empty token")
	}
}

func TestHandleUploadValid(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, []string{".mp3", ".wav"}, testFeedMetadata(), log.New(io.Discard, "", 0))

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "episode.mp3")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	part.Write([]byte("fake audio data"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/ui/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(filepath.Join(audioDir, "episode.mp3")); err != nil {
		t.Fatalf("uploaded file should exist: %v", err)
	}
}

func TestHandleUploadRejectsNonPOST(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/ui/upload", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleUploadDisallowedExtension(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, []string{".mp3"}, testFeedMetadata(), log.New(io.Discard, "", 0))

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "malware.exe")
	part.Write([]byte("bad"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/ui/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for disallowed extension, got %d", rec.Code)
	}
}

func TestHandleUploadDuplicateFile(t *testing.T) {
	audioDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(audioDir, "dup.mp3"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}
	handler := New(&fakeLibrary{}, nil, audioDir, []string{".mp3"}, testFeedMetadata(), log.New(io.Discard, "", 0))

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "dup.mp3")
	part.Write([]byte("new data"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/ui/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestHandleUploadMissingFile(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, []string{".mp3"}, testFeedMetadata(), log.New(io.Discard, "", 0))

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/ui/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing file field, got %d", rec.Code)
	}
}

func TestAudioEndpointDelete(t *testing.T) {
	audioDir := t.TempDir()
	filePath := filepath.Join(audioDir, "todelete.mp3")
	if err := os.WriteFile(filePath, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write audio file: %v", err)
	}

	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodDelete, "/audio/todelete.mp3", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted")
	}
}

func TestAudioEndpointDeleteRequiresToken(t *testing.T) {
	audioDir := t.TempDir()
	filePath := filepath.Join(audioDir, "clip.mp3")
	if err := os.WriteFile(filePath, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write audio file: %v", err)
	}

	validator := &fakeValidator{allowed: map[string]struct{}{"secret": {}}}
	handler := New(&fakeLibrary{}, validator, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodDelete, "/audio/clip.mp3", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for delete without token, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/audio/clip.mp3?token=secret", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 with token, got %d", rec.Code)
	}
}

func TestAudioEndpointNotFound(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/audio/nonexistent.mp3", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAudioEndpointRejectsDirectory(t *testing.T) {
	audioDir := t.TempDir()
	subdir := filepath.Join(audioDir, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/audio/subdir", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for directory, got %d", rec.Code)
	}
}

func TestAudioEndpointRejectsUnsupportedMethod(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodPut, "/audio/clip.mp3", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestAudioEndpointHeadRequest(t *testing.T) {
	audioDir := t.TempDir()
	filePath := filepath.Join(audioDir, "clip.mp3")
	if err := os.WriteFile(filePath, []byte("audio-bytes"), 0o644); err != nil {
		t.Fatalf("write audio file: %v", err)
	}

	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodHead, "/audio/clip.mp3", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		seconds float64
		want    string
	}{
		{0, ""},
		{-5, ""},
		{61.0, "00:01:01"},
		{3661.0, "01:01:01"},
		{59.5, "00:01:00"},
	}

	for _, tc := range tests {
		got := formatDuration(tc.seconds)
		if got != tc.want {
			t.Fatalf("formatDuration(%v) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func TestEpisodeDescription(t *testing.T) {
	ep := models.Episode{Filename: "song.mp3"}
	if got := episodeDescription(ep); got != "song.mp3" {
		t.Fatalf("expected just filename, got %q", got)
	}

	artist := "Artist"
	album := "Album"
	ep = models.Episode{Filename: "song.mp3", Artist: &artist, Album: &album}
	got := episodeDescription(ep)
	if !strings.Contains(got, "Artist") || !strings.Contains(got, "Album") || !strings.Contains(got, "song.mp3") {
		t.Fatalf("expected artist, album, filename in description, got %q", got)
	}
}

func TestFeedEndpointEmptyHost(t *testing.T) {
	audioDir := t.TempDir()
	handler := New(&fakeLibrary{}, nil, audioDir, nil, testFeedMetadata(), log.New(io.Discard, "", 0))

	req := httptest.NewRequest(http.MethodGet, "/feed", nil)
	req.Host = ""
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for empty host, got %d", rec.Code)
	}
}
