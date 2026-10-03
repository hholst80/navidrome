package metadata

import (
	"cmp"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/metadata/cue"
)

// CUETracks expands a lossless WAV, FLAC or APE album image. The source path remains real;
// CueTrack distinguishes the virtual tracks in persistence. No audio is modified.
func (md Metadata) CUETracks(sheet *cue.Cuesheet, libID int, folderID string, totalSamples int64) (model.MediaFiles, error) {
	if md.Suffix() != "wav" && md.Suffix() != "flac" && md.Suffix() != "ape" {
		return nil, fmt.Errorf("CUE source must be PCM WAV, FLAC or APE")
	}
	if len(sheet.File) != 1 || len(sheet.File[0].Tracks) == 0 {
		return nil, fmt.Errorf("CUE must describe one audio file with tracks")
	}
	file := sheet.File[0]
	if strings.ContainsAny(file.FileName, "/\\") || file.FileName != path.Base(md.FilePath()) {
		return nil, fmt.Errorf("CUE FILE must name the audio file in the same directory")
	}
	starts, err := md.cueBoundaries(file, totalSamples)
	if err != nil {
		return nil, err
	}
	rate := md.audioProps.SampleRate
	tracks := make(model.MediaFiles, 0, len(file.Tracks))
	for i, t := range file.Tracks {
		raw := model.RawTags{}
		for key, values := range md.tags {
			raw[key.String()] = append([]string(nil), values...)
		}
		for _, key := range []model.TagName{model.TagTitle, model.TagTrackNumber, model.TagLyrics, model.TagMusicBrainzRecordingID, model.TagMusicBrainzTrackID, model.TagReplayGainTrackGain, model.TagReplayGainTrackPeak} {
			delete(raw, key.String())
		}
		set := func(key model.TagName, value string) {
			if value != "" {
				raw[key.String()] = []string{value}
			}
		}
		set(model.TagTitle, cmp.Or(t.Title, fmt.Sprintf("Track %02d", t.TrackNumber)))
		set(model.TagAlbum, sheet.Title)
		set(model.TagAlbumArtist, sheet.Performer)
		set(model.TagTrackArtist, cmp.Or(t.Performer, sheet.Performer))
		set(model.TagTrackNumber, strconv.Itoa(int(t.TrackNumber)))
		set(model.TagTotalTracks, strconv.Itoa(len(file.Tracks)))
		if sheet.Rem.DiscNumber() > 0 || sheet.Rem.TotalDiscs() > 0 {
			disc, total := md.NumAndTotal(model.TagDiscNumber)
			if n := sheet.Rem.DiscNumber(); n > 0 {
				disc = n
			}
			if n := sheet.Rem.TotalDiscs(); n > 0 {
				total = n
			}
			set(model.TagDiscNumber, fmt.Sprintf("%d/%d", disc, total))
			if total > 0 {
				set(model.TagTotalDiscs, strconv.Itoa(total))
			}
		}
		set(model.TagGenre, sheet.Rem.Genre())
		set(model.TagRecordingDate, sheet.Rem.Date())
		set(model.TagISRC, t.ISRC)
		set(model.TagComment, cmp.Or(t.Rem.Comment(), sheet.Rem.Comment()))
		set(model.TagReplayGainAlbumGain, sheet.Rem.AlbumGain())
		set(model.TagReplayGainAlbumPeak, sheet.Rem.AlbumPeak())
		set(model.TagReplayGainTrackGain, t.Rem.TrackGain())
		set(model.TagReplayGainTrackPeak, t.Rem.TrackPeak())
		props := md.audioProps
		end := totalSamples
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		if end > 0 {
			props.Duration = time.Duration(float64(end-starts[i]) / float64(rate) * float64(time.Second))
		} else {
			props.Duration -= time.Duration(float64(starts[i]) / float64(rate) * float64(time.Second))
		}
		trackMD := New(md.filePath, Info{Tags: raw, FileInfo: md.fileInfo, AudioProperties: props, HasPicture: md.hasPicture})
		mf := trackMD.ToMediaFile(libID, folderID)
		mf.CueTrack, mf.CueStartSample, mf.CueEndSample = int(t.TrackNumber), starts[i], end
		tracks = append(tracks, mf)
	}
	return tracks, nil
}

func (md Metadata) cueBoundaries(file cue.File, totalSamples int64) ([]int64, error) {
	rate := md.audioProps.SampleRate
	if rate <= 0 || rate%75 != 0 || totalSamples < 0 {
		return nil, fmt.Errorf("CUE sample rate must be a positive multiple of 75")
	}
	starts := make([]int64, len(file.Tracks))
	for i, t := range file.Tracks {
		if t.TrackDataType != "AUDIO" || t.PreGap != 0 || t.PostGap != 0 || t.Flags&cue.Pre != 0 {
			return nil, fmt.Errorf("CUE data tracks, synthetic gaps and pre-emphasis are unsupported")
		}
		found := false
		for _, index := range t.Index {
			if index.Number != 1 {
				continue
			}
			// Compare before multiplying to prevent overflow from hostile timestamps.
			if (totalSamples > 0 && uint64(index.Frame) > uint64(totalSamples-1)/uint64(rate/75)) ||
				(totalSamples == 0 && float64(index.Frame)/75 >= md.audioProps.Duration.Seconds()) {
				return nil, fmt.Errorf("CUE track %d starts outside the audio", t.TrackNumber)
			}
			starts[i] = int64(index.Frame) * int64(rate/75)
			found = true
		}
		if !found {
			return nil, fmt.Errorf("CUE track %d has no INDEX 01", t.TrackNumber)
		}
		if i > 0 && starts[i] <= starts[i-1] {
			return nil, fmt.Errorf("CUE track boundaries must increase")
		}
	}
	// Preserve all source samples: any first-track pregap belongs to track one;
	// subsequent INDEX 00 audio stays at the end of the preceding track.
	starts[0] = 0
	return starts, nil
}
