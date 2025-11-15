package models

import "time"

// Episode represents the metadata exposed for a single audio file.
type Episode struct {
	ID              string    `json:"id"`
	Filename        string    `json:"filename"`
	RelativePath    string    `json:"relative_path"`
	Title           string    `json:"title"`
	Artist          *string   `json:"artist,omitempty"`
	Album           *string   `json:"album,omitempty"`
	DurationSeconds *float64  `json:"duration_seconds,omitempty"`
	BitrateKbps     *int      `json:"bitrate_kbps,omitempty"`
	FilesizeBytes   int64     `json:"filesize_bytes"`
	ModifiedAt      time.Time `json:"modified_at"`
}
