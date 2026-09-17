package stream

import (
	"github.com/navidrome/navidrome/core/ffmpeg"
	"github.com/navidrome/navidrome/model"
	"strconv"
)

func cueSegment(mf *model.MediaFile) *ffmpeg.AudioSegment {
	if mf.CueTrack == 0 {
		return nil
	}
	return &ffmpeg.AudioSegment{
		StartSample: mf.CueStartSample, EndSample: mf.CueEndSample, SourceRate: mf.SampleRate,
		Tags: map[string]string{
			"title": mf.Title, "artist": mf.Artist, "album": mf.Album, "album_artist": mf.AlbumArtist,
			"track": strconv.Itoa(mf.TrackNumber), "disc": strconv.Itoa(mf.DiscNumber), "date": mf.Date,
		},
	}
}
