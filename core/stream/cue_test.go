package stream

import (
	"testing"

	"github.com/navidrome/navidrome/model"
	"github.com/stretchr/testify/assert"
)

func TestCUECacheMetadata(t *testing.T) {
	original := model.MediaFile{ID: "cue-track", CueTrack: 1, Title: "First", Artist: "Artist", Album: "Album",
		AlbumArtist: "Album Artist", TrackNumber: 1, DiscNumber: 1, Date: "2026"}
	key := (&streamJob{mf: &original}).Key()
	for i := 0; i < 20; i++ {
		assert.Equal(t, key, (&streamJob{mf: &original}).Key())
	}
	for name, change := range map[string]func(*model.MediaFile){
		"title":        func(m *model.MediaFile) { m.Title = "Second" },
		"artist":       func(m *model.MediaFile) { m.Artist = "Other" },
		"album":        func(m *model.MediaFile) { m.Album = "Other" },
		"album artist": func(m *model.MediaFile) { m.AlbumArtist = "Other" },
		"track":        func(m *model.MediaFile) { m.TrackNumber++ },
		"disc":         func(m *model.MediaFile) { m.DiscNumber++ },
		"date":         func(m *model.MediaFile) { m.Date = "2025" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := original
			change(&changed)
			assert.NotEqual(t, key, (&streamJob{mf: &changed}).Key())
			originalFile, changedFile := original, changed
			originalFile.CueTrack, changedFile.CueTrack = 0, 0
			assert.Equal(t, (&streamJob{mf: &originalFile}).Key(), (&streamJob{mf: &changedFile}).Key())
		})
	}
}
