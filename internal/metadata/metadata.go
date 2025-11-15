package metadata

import (
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dhowden/tag"
	"github.com/tcolgate/mp3"

	"home-podcast/internal/models"
)

// BuildEpisode constructs a metadata snapshot for the given audio file path.
func BuildEpisode(path string, root string) (models.Episode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return models.Episode{}, err
	}

	relative, err := filepath.Rel(root, path)
	if err != nil {
		relative = filepath.Base(path)
	}
	relative = filepath.ToSlash(relative)

	title, artist, album := readTags(path)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	var durationPtr *float64
	var bitratePtr *int

	if strings.EqualFold(filepath.Ext(path), ".mp3") {
		dur, err := computeMP3Duration(path)
		if err == nil && dur > 0 {
			duration := dur
			durationPtr = &duration

			bitrate := int(math.Round((float64(info.Size()) * 8) / duration / 1000))
			if bitrate > 0 {
				bitratePtr = &bitrate
			}
		}
	}

	return models.Episode{
		ID:              relative,
		Filename:        filepath.Base(path),
		RelativePath:    relative,
		Title:           title,
		Artist:          artist,
		Album:           album,
		DurationSeconds: durationPtr,
		BitrateKbps:     bitratePtr,
		FilesizeBytes:   info.Size(),
		ModifiedAt:      info.ModTime().UTC().Round(time.Second),
	}, nil
}

func readTags(path string) (string, *string, *string) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, nil
	}
	defer f.Close()

	meta, err := tag.ReadFrom(f)
	if err != nil {
		return "", nil, nil
	}

	title := strings.TrimSpace(meta.Title())
	artist := optionalString(meta.Artist())
	album := optionalString(meta.Album())
	return title, artist, album
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func computeMP3Duration(path string) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	decoder := mp3.NewDecoder(f)
	var frame mp3.Frame
	var skipped int
	var total float64

	for {
		err := decoder.Decode(&frame, &skipped)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}
		total += frame.Duration().Seconds()
	}

	return total, nil
}
