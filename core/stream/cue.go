package stream

import (
	"strconv"
	"strings"

	"github.com/navidrome/navidrome/core/ffmpeg"
	"github.com/navidrome/navidrome/model"
)

// OutputFormat resolves the container used for playback or an extracted track.
// Original album-image downloads bypass streaming and retain the source format.
func OutputFormat(mf *model.MediaFile, requested string) string {
	if requested != "" && requested != "raw" {
		return requested
	}
	if mf.CueTrack > 0 && strings.EqualFold(mf.Suffix, "ape") {
		return "flac"
	}
	return mf.Suffix
}

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
