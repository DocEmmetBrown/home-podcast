package server

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"home-podcast/internal/models"
)

// EpisodeProvider abstracts the episode source for the HTTP handlers.
type EpisodeProvider interface {
	ListEpisodes() []models.Episode
}

// TokenValidator determines whether a supplied token is authorized.
type TokenValidator interface {
	IsValidToken(token string) bool
}

// FeedMetadata describes the static information necessary to render the RSS feed.
type FeedMetadata struct {
	Title       string
	Description string
	Language    string
	Author      string
}

type serverHandler struct {
	lib       EpisodeProvider
	validator TokenValidator
	audioRoot string
	feed      FeedMetadata
	logger    *log.Logger
	allowed   map[string]struct{}
}

// New creates the HTTP handler that exposes the library API and RSS feed.
func New(lib EpisodeProvider, validator TokenValidator, audioRoot string, allowedExtensions []string, feed FeedMetadata, logger *log.Logger) http.Handler {
	if logger == nil {
		logger = log.Default()
	}

	cleanRoot := filepath.Clean(audioRoot)
	absRoot, err := filepath.Abs(cleanRoot)
	if err != nil {
		logger.Printf("warning: unable to resolve absolute audio root %q: %v", audioRoot, err)
		absRoot = cleanRoot
	}

	// Apply sane defaults if configuration omitted specific values.
	if feed.Title == "" {
		feed.Title = "Home Podcast"
	}
	if feed.Description == "" {
		feed.Description = feed.Title
	}

	h := &serverHandler{
		lib:       lib,
		validator: validator,
		audioRoot: absRoot,
		feed:      feed,
		logger:    logger,
		allowed:   make(map[string]struct{}, len(allowedExtensions)),
	}
	for _, ext := range allowedExtensions {
		h.allowed[strings.ToLower(ext)] = struct{}{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/episodes", h.handleEpisodes)
	mux.HandleFunc("/feed", h.handleFeed)
	mux.HandleFunc("/feed.xml", h.handleFeed)
	mux.HandleFunc("/rss", h.handleFeed)
	mux.HandleFunc("/ui", h.handleUI)
	mux.HandleFunc("/ui/upload", h.handleUpload)
	mux.HandleFunc("/audio/", h.handleAudio)

	return logRequests(mux, logger)
}

func (h *serverHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *serverHandler) handleEpisodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if _, ok := h.requireToken(w, r); !ok {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	episodes := h.lib.ListEpisodes()
	if err := json.NewEncoder(w).Encode(episodes); err != nil {
		h.logger.Printf("failed to encode episodes: %v", err)
	}
}

func (h *serverHandler) handleFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	token, ok := h.requireToken(w, r)
	if !ok {
		return
	}

	base := h.requestBaseURL(r)
	if base == nil {
		h.logger.Printf("unable to determine request base URL")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	data, err := h.buildRSSFeed(base, r.URL.Path, r.URL.RawQuery, h.lib.ListEpisodes(), token)
	if err != nil {
		h.logger.Printf("failed to build RSS feed: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	if _, err := w.Write(data); err != nil {
		h.logger.Printf("failed to write RSS feed: %v", err)
	}
}

func (h *serverHandler) handleUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	token, ok := h.requireToken(w, r)
	if !ok {
		return
	}

	setAuthCookie(w, r, token)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write([]byte(uiPage)); err != nil {
		h.logger.Printf("failed to write UI page: %v", err)
	}
}

func (h *serverHandler) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if _, ok := h.requireToken(w, r); !ok {
		return
	}

	if err := r.ParseMultipartForm(200 << 20); err != nil {
		h.logger.Printf("upload parse error: %v", err)
		http.Error(w, "invalid upload form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		h.httpError(w, "missing file", http.StatusBadRequest, err)
		return
	}
	defer file.Close()

	name := filepath.Base(header.Filename)
	if name == "" {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	ext := strings.ToLower(filepath.Ext(name))
	if _, ok := h.allowed[ext]; !ok {
		http.Error(w, "unsupported file type", http.StatusBadRequest)
		return
	}

	dest := filepath.Join(h.audioRoot, name)
	if _, err := os.Stat(dest); err == nil {
		http.Error(w, "file already exists", http.StatusConflict)
		return
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		h.httpError(w, "stat error", http.StatusInternalServerError, err)
		return
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		h.httpError(w, "unable to create file", http.StatusInternalServerError, err)
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		_ = os.Remove(dest)
		h.httpError(w, "write error", http.StatusInternalServerError, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *serverHandler) handleAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if r.Method == http.MethodDelete {
		if _, ok := h.requireToken(w, r); !ok {
			return
		}
	} else {
		if _, ok := h.requireToken(w, r); !ok {
			return
		}
	}

	rel := strings.TrimPrefix(r.URL.Path, "/audio/")
	rel = pathpkg.Clean(rel)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || rel == "." {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	target := filepath.Join(h.audioRoot, filepath.FromSlash(rel))
	resolved, err := filepath.Abs(target)
	if err != nil {
		h.logger.Printf("failed to resolve audio path %s: %v", target, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if !pathWithinRoot(h.audioRoot, resolved) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h.logger.Printf("failed to stat audio file %s: %v", resolved, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if info.IsDir() {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if r.Method == http.MethodDelete {
		if err := os.Remove(resolved); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			h.logger.Printf("failed to delete audio file %s: %v", resolved, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.ServeFile(w, r, resolved)
}

func (h *serverHandler) requireToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.validator == nil {
		return "", true
	}

	token := extractToken(r)
	if token == "" || !h.validator.IsValidToken(token) {
		w.WriteHeader(http.StatusUnauthorized)
		return "", false
	}
	return token, true
}

func (h *serverHandler) requestBaseURL(r *http.Request) *url.URL {
	scheme := "http"
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			candidate := strings.TrimSpace(parts[0])
			if candidate != "" {
				scheme = candidate
			}
		}
	} else if r.TLS != nil {
		scheme = "https"
	}

	host := strings.TrimSpace(r.Host)
	if host == "" {
		return nil
	}

	return &url.URL{Scheme: scheme, Host: host}
}

func (h *serverHandler) buildRSSFeed(base *url.URL, requestPath, rawQuery string, episodes []models.Episode, token string) ([]byte, error) {
	feedURL := *base
	feedURL.Path = requestPath
	feedURL.RawQuery = rawQuery

	channelLink := *base
	channelLink.Path = ""
	channelLink.RawQuery = ""

	sorted := make([]models.Episode, len(episodes))
	copy(sorted, episodes)
	sort.SliceStable(sorted, func(i, j int) bool {
		iTime := sorted[i].ModifiedAt
		jTime := sorted[j].ModifiedAt
		if iTime.Equal(jTime) {
			return sorted[i].ID > sorted[j].ID
		}
		return iTime.After(jTime)
	})

	lastBuild := time.Time{}
	for _, ep := range sorted {
		if !ep.ModifiedAt.IsZero() && (lastBuild.IsZero() || ep.ModifiedAt.After(lastBuild)) {
			lastBuild = ep.ModifiedAt.UTC()
		}
	}
	if lastBuild.IsZero() {
		lastBuild = time.Now().UTC()
	}

	rss := rssFeed{
		Version:  "2.0",
		AtomNS:   "http://www.w3.org/2005/Atom",
		ITunesNS: "http://www.itunes.com/dtds/podcast-1.0.dtd",
		Channel: rssChannel{
			Title:         h.feed.Title,
			Link:          channelLink.String(),
			Description:   h.feed.Description,
			Language:      h.feed.Language,
			LastBuildDate: lastBuild.Format(time.RFC1123Z),
			Generator:     "home-podcast",
			AtomLink: rssAtomLink{
				Href: feedURL.String(),
				Rel:  "self",
				Type: "application/rss+xml",
			},
		},
	}

	if h.feed.Author != "" {
		rss.Channel.ITunesAuthor = h.feed.Author
	}

	for _, ep := range sorted {
		enclosureURL := *base
		enclosureURL.Path = "/" + strings.TrimLeft(pathpkg.Join("audio", ep.RelativePath), "/")
		enclosureURL.RawQuery = ""
		if token != "" {
			values := enclosureURL.Query()
			values.Set("token", token)
			enclosureURL.RawQuery = values.Encode()
		}

		enclosureURL.Scheme = "https"

		item := rssItem{
			Title: ep.Title,
			Link:  enclosureURL.String(),
			GUID:  rssGUID{IsPermaLink: "false", Value: ep.ID},
			PubDate: func() string {
				if ep.ModifiedAt.IsZero() {
					return ""
				}
				return ep.ModifiedAt.UTC().Format(time.RFC1123Z)
			}(),
			Description: episodeDescription(ep),
			Enclosure: rssEnclosure{
				URL:    enclosureURL.String(),
				Length: ep.FilesizeBytes,
				Type:   mimeTypeForFilename(ep.Filename),
			},
		}

		if ep.DurationSeconds != nil {
			if formatted := formatDuration(*ep.DurationSeconds); formatted != "" {
				item.ITunesDuration = formatted
			}
		}

		if ep.Artist != nil {
			item.ITunesAuthor = *ep.Artist
		} else if h.feed.Author != "" {
			item.ITunesAuthor = h.feed.Author
		}

		rss.Channel.Items = append(rss.Channel.Items, item)
	}

	output, err := xml.MarshalIndent(rss, "", "  ")
	if err != nil {
		return nil, err
	}

	return append([]byte(xml.Header), output...), nil
}

type statusWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

func logRequests(next http.Handler, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		duration := time.Since(start)
		logger.Printf("%s %s -> %d (%dB) in %s", r.Method, r.URL.Path, sw.status, sw.size, duration)
	})
}

func extractToken(r *http.Request) string {
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		return token
	}

	if header := strings.TrimSpace(r.Header.Get("X-Podcast-Token")); header != "" {
		return header
	}

	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if authz == "" {
		return ""
	}

	if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
		return strings.TrimSpace(authz[7:])
	}

	if cookie, err := r.Cookie(authCookieName); err == nil {
		if value := strings.TrimSpace(cookie.Value); value != "" {
			return value
		}
	}

	return ""
}

func (h *serverHandler) httpError(w http.ResponseWriter, userMsg string, status int, err error) {
	if err != nil {
		h.logger.Printf("%s: %v", userMsg, err)
	}
	http.Error(w, userMsg, status)
}

const authCookieName = "podcast_token"

func setAuthCookie(w http.ResponseWriter, r *http.Request, token string) {
	if token == "" {
		return
	}
	cookie := &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPSRequest(r),
	}
	http.SetCookie(w, cookie)
}

func isHTTPSRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if forwarded == "" {
		return false
	}
	parts := strings.Split(forwarded, ",")
	if len(parts) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(parts[0]), "https")
}

func pathWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel != ".." && !strings.HasPrefix(rel, "../")
}

func episodeDescription(ep models.Episode) string {
	parts := make([]string, 0, 3)
	if ep.Artist != nil && *ep.Artist != "" {
		parts = append(parts, *ep.Artist)
	}
	if ep.Album != nil && *ep.Album != "" {
		parts = append(parts, *ep.Album)
	}
	parts = append(parts, ep.Filename)
	return strings.Join(parts, " – ")
}

func mimeTypeForFilename(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext != "" {
		if value := mime.TypeByExtension(ext); value != "" {
			return value
		}
		if fallback, ok := fallbackMIMETypes[ext]; ok {
			return fallback
		}
	}
	return "application/octet-stream"
}

var fallbackMIMETypes = map[string]string{
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".flac": "audio/flac",
	".ogg":  "audio/ogg",
}

func formatDuration(seconds float64) string {
	if seconds <= 0 {
		return ""
	}
	total := int64(seconds + 0.5)
	hours := total / 3600
	minutes := (total % 3600) / 60
	secs := total % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
}

type rssFeed struct {
	XMLName  xml.Name   `xml:"rss"`
	Version  string     `xml:"version,attr"`
	AtomNS   string     `xml:"xmlns:atom,attr"`
	ITunesNS string     `xml:"xmlns:itunes,attr"`
	Channel  rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string      `xml:"title"`
	Link          string      `xml:"link"`
	Description   string      `xml:"description"`
	Language      string      `xml:"language,omitempty"`
	LastBuildDate string      `xml:"lastBuildDate"`
	Generator     string      `xml:"generator"`
	AtomLink      rssAtomLink `xml:"atom:link"`
	ITunesAuthor  string      `xml:"itunes:author,omitempty"`
	Items         []rssItem   `xml:"item"`
}

type rssAtomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssItem struct {
	Title          string       `xml:"title"`
	Link           string       `xml:"link"`
	GUID           rssGUID      `xml:"guid"`
	PubDate        string       `xml:"pubDate,omitempty"`
	Description    string       `xml:"description"`
	Enclosure      rssEnclosure `xml:"enclosure"`
	ITunesDuration string       `xml:"itunes:duration,omitempty"`
	ITunesAuthor   string       `xml:"itunes:author,omitempty"`
}

type rssGUID struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

const uiPage = `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width,initial-scale=1">
	<title>Home Podcast Library</title>
	<style>
		body { font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 2rem; background: #f5f6f8; color: #1a1c1f; }
		h1 { margin-bottom: 1.5rem; }
		section { background: #fff; border-radius: 8px; padding: 1.5rem; box-shadow: 0 2px 4px rgba(0,0,0,0.08); margin-bottom: 2rem; }
		button, input[type="submit"] { cursor: pointer; background: #1a73e8; color: #fff; border: none; border-radius: 4px; padding: 0.6rem 1.1rem; font-size: 0.95rem; }
		button.secondary { background: #5f6368; }
		button:disabled { background: #9aa0a6; cursor: not-allowed; }
		table { width: 100%; border-collapse: collapse; margin-top: 1rem; }
		th, td { padding: 0.6rem; border-bottom: 1px solid #e0e0e0; text-align: left; }
		th { background: #f0f2f5; text-transform: uppercase; font-size: 0.75rem; letter-spacing: .05em; }
		.actions { display: flex; gap: 0.5rem; }
		#status { margin-top: 0.75rem; font-size: 0.9rem; }
		.success { color: #0b8043; }
		.error { color: #d93025; }
	</style>
</head>
<body>
	<h1>Home Podcast Library</h1>

	<section>
		<h2>Upload New Episode</h2>
		<form id="uploadForm">
			<input type="file" id="fileInput" name="file" accept="audio/*" required>
			<input type="submit" value="Upload">
			<span id="uploadStatus"></span>
		</form>
	</section>

	<section>
		<div style="display:flex;justify-content:space-between;align-items:center;">
			<h2>Episodes</h2>
			<div>
				<button type="button" id="refreshBtn">Refresh</button>
			</div>
		</div>
		<div id="status"></div>
		<table id="episodesTable" aria-live="polite">
			<thead>
				<tr>
					<th>Title</th>
					<th>File</th>
					<th>Modified</th>
					<th>Size</th>
					<th>Actions</th>
				</tr>
			</thead>
			<tbody>
				<tr><td colspan="5">Loading…</td></tr>
			</tbody>
		</table>
	</section>

	<script>
		const statusEl = document.getElementById('status');
		const tableBody = document.querySelector('#episodesTable tbody');
		const uploadForm = document.getElementById('uploadForm');
		const uploadStatus = document.getElementById('uploadStatus');

		function formatDate(value) {
			if (!value) return '';
			const date = new Date(value);
			if (Number.isNaN(date.getTime())) return value;
			return date.toLocaleString();
		}

		function formatSize(bytes) {
			if (!Number.isFinite(bytes)) return '';
			if (bytes < 1024) return bytes + ' B';
			const units = ['KB','MB','GB'];
			let size = bytes / 1024;
			let unit = units[0];
			for (let i=0; i<units.length && size >= 1024; i++) {
				size /= 1024;
				unit = units[i];
			}
			return size.toFixed(1) + ' ' + unit;
		}

		function encodePath(relPath) {
			return relPath.split('/').map(encodeURIComponent).join('/');
		}

		async function loadEpisodes() {
			statusEl.textContent = '';
			try {
				const res = await fetch('/episodes', { credentials: 'include' });
				if (!res.ok) throw new Error('Request failed with ' + res.status);
				const data = await res.json();
				if (!Array.isArray(data)) throw new Error('Unexpected response');
				renderEpisodes(data);
			} catch (err) {
				tableBody.innerHTML = '<tr><td colspan="5">Failed to load episodes.</td></tr>';
				statusEl.textContent = err.message;
				statusEl.className = 'error';
			}
		}

		function renderEpisodes(items) {
			if (items.length === 0) {
				tableBody.innerHTML = '<tr><td colspan="5">No episodes found.</td></tr>';
				return;
			}

			tableBody.innerHTML = '';
			for (const item of items) {
				const tr = document.createElement('tr');
				const path = item.relative_path || item.RelativePath || '';
				const linkPath = encodePath(path);
				const href = '/audio/' + linkPath;

				const titleCell = document.createElement('td');
				titleCell.textContent = item.title || '';
				tr.appendChild(titleCell);

				const fileCell = document.createElement('td');
				const link = document.createElement('a');
				link.href = href;
				link.target = '_blank';
				link.rel = 'noopener';
				link.textContent = item.filename || '';
				fileCell.appendChild(link);
				tr.appendChild(fileCell);

				const modifiedCell = document.createElement('td');
				modifiedCell.textContent = formatDate(item.modified_at || item.ModifiedAt);
				tr.appendChild(modifiedCell);

				const sizeCell = document.createElement('td');
				sizeCell.textContent = formatSize(item.filesize_bytes || item.FilesizeBytes);
				tr.appendChild(sizeCell);

				const actionsCell = document.createElement('td');
				actionsCell.className = 'actions';
				const deleteButton = document.createElement('button');
				deleteButton.type = 'button';
				deleteButton.className = 'secondary';
				deleteButton.dataset.path = linkPath;
				deleteButton.textContent = 'Delete';
				deleteButton.addEventListener('click', async () => {
					const rel = deleteButton.dataset.path || '';
					if (!confirm('Delete this episode from storage?')) return;
					try {
						const res = await fetch('/audio/' + rel, { method: 'DELETE', credentials: 'include' });
						if (res.status !== 204) throw new Error('Delete failed with ' + res.status);
						await loadEpisodes();
						statusEl.textContent = 'Episode deleted';
						statusEl.className = 'success';
					} catch (err) {
						statusEl.textContent = err.message;
						statusEl.className = 'error';
					}
				});
				actionsCell.appendChild(deleteButton);
				tr.appendChild(actionsCell);

				tableBody.appendChild(tr);
			}
		}

		uploadForm.addEventListener('submit', async (event) => {
			event.preventDefault();
			const input = document.getElementById('fileInput');
			if (!input.files || input.files.length === 0) {
				uploadStatus.textContent = 'Select an audio file first.';
				uploadStatus.className = 'error';
				return;
			}

			const formData = new FormData();
			formData.append('file', input.files[0]);

			uploadStatus.textContent = 'Uploading…';
			uploadStatus.className = '';

			try {
				const res = await fetch('/ui/upload', { method: 'POST', body: formData, credentials: 'include' });
				if (!res.ok) throw new Error('Upload failed with ' + res.status);
				uploadStatus.textContent = 'Upload complete';
				uploadStatus.className = 'success';
				input.value = '';
				await loadEpisodes();
			} catch (err) {
				uploadStatus.textContent = err.message;
				uploadStatus.className = 'error';
			}
		});

		document.getElementById('refreshBtn').addEventListener('click', loadEpisodes);

		loadEpisodes();
	</script>
</body>
</html>`
