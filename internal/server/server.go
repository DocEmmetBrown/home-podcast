package server

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
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
}

// New creates the HTTP handler that exposes the library API and RSS feed.
func New(lib EpisodeProvider, validator TokenValidator, audioRoot string, feed FeedMetadata, logger *log.Logger) http.Handler {
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
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/episodes", h.handleEpisodes)
	mux.HandleFunc("/feed", h.handleFeed)
	mux.HandleFunc("/feed.xml", h.handleFeed)
	mux.HandleFunc("/rss", h.handleFeed)
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

func (h *serverHandler) handleAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if _, ok := h.requireToken(w, r); !ok {
		return
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

	return ""
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
