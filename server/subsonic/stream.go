package subsonic

import (
	"errors"
	"fmt"
	"github.com/Masterminds/squirrel"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/core"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/request"
	"github.com/navidrome/navidrome/server/subsonic/responses"
	"github.com/navidrome/navidrome/utils/req"
)

func (api *Router) Stream(w http.ResponseWriter, r *http.Request) (*responses.Subsonic, error) {
	ctx := r.Context()
	p := req.Params(r)
	id, err := p.String("id")
	if err != nil {
		return nil, err
	}
	maxBitRate := p.IntOr("maxBitRate", 0)
	format, _ := p.String("format")
	timeOffset := p.IntOr("timeOffset", 0)

	mf, err := api.ds.MediaFile(ctx).Get(id)
	if err != nil {
		return nil, err
	}

	streamReq := api.transcodeDecision.ResolveRequest(ctx, mf, format, maxBitRate, timeOffset)
	stream, err := api.streamer.NewStream(ctx, mf, streamReq)
	if err != nil {
		return nil, err
	}

	// Make sure the stream will be closed at the end, to avoid leakage
	defer func() {
		if err := stream.Close(); err != nil && log.IsGreaterOrEqualTo(log.LevelDebug) {
			log.Error("Error closing stream", "id", id, "file", stream.Name(), err)
		}
	}()

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Content-Duration", strconv.FormatFloat(float64(stream.Duration()), 'G', -1, 32))

	_, err = stream.Serve(ctx, w, r)
	return nil, err
}

func (api *Router) Download(w http.ResponseWriter, r *http.Request) (*responses.Subsonic, error) {
	ctx := r.Context()
	username, _ := request.UsernameFrom(ctx)
	p := req.Params(r)
	id, err := p.String("id")
	if err != nil {
		return nil, err
	}

	if !conf.Server.EnableDownloads {
		log.Warn(ctx, "Downloads are disabled", "user", username, "id", id)
		return nil, newError(responses.ErrorAuthorizationFail, "downloads are disabled")
	}

	entity, err := model.GetEntityByID(ctx, api.ds, id)
	if err != nil {
		return nil, err
	}

	maxBitRate := p.IntOr("bitrate", 0)
	format, _ := p.String("format")

	if format == "" {
		if conf.Server.AutoTranscodeDownload {
			// if we are not provided a format, see if we have requested transcoding for this client
			// This must be enabled via a config option. For the UI, we are always given an option.
			// This will impact other clients which do not use the UI
			transcoding, ok := request.TranscodingFrom(ctx)

			if !ok {
				format = "raw"
			} else {
				format = transcoding.TargetFormat
				maxBitRate = transcoding.DefaultBitRate
			}
		} else {
			format = "raw"
		}
	}

	setHeaders := func(name string) {
		name = strings.ReplaceAll(name, ",", "_")
		disposition := fmt.Sprintf("attachment; filename=\"%s.zip\"", name)
		w.Header().Set("Content-Disposition", disposition)
		w.Header().Set("Content-Type", "application/zip")
	}

	switch v := entity.(type) {
	case *model.MediaFile:
		if format == "raw" && v.CueTrack > 0 {
			return nil, serveOriginalCUE(w, r, v)
		}
		streamReq := api.transcodeDecision.ResolveRequest(ctx, v, format, maxBitRate, 0)
		stream, err := api.streamer.NewStream(ctx, v, streamReq)
		if err != nil {
			return nil, err
		}

		// Make sure the stream will be closed at the end, to avoid leakage
		defer func() {
			if err := stream.Close(); err != nil && log.IsGreaterOrEqualTo(log.LevelDebug) {
				log.Error("Error closing stream", "id", id, "file", stream.Name(), err)
			}
		}()

		disposition := fmt.Sprintf("attachment; filename=\"%s\"", stream.Name())
		w.Header().Set("Content-Disposition", disposition)

		_, err = stream.Serve(ctx, w, r)
		return nil, err
	case *model.Album:
		if format == "raw" {
			tracks, err := api.ds.MediaFile(ctx).GetAll(model.QueryOptions{Filters: squirrel.Eq{"album_id": id}})
			if err != nil {
				return nil, err
			}
			if source := singleCUESource(tracks); source != nil {
				return nil, serveOriginalCUE(w, r, source)
			}
		}
		setHeaders(v.Name)
		return nil, handleArchiveErr(w, api.archiver.ZipAlbum(ctx, id, format, maxBitRate, w))
	case *model.Artist:
		setHeaders(v.Name)
		return nil, handleArchiveErr(w, api.archiver.ZipArtist(ctx, id, format, maxBitRate, w))
	case *model.Playlist:
		setHeaders(v.Name)
		return nil, handleArchiveErr(w, api.archiver.ZipPlaylist(ctx, id, format, maxBitRate, w))
	default:
		return nil, model.ErrNotFound
	}
}

// Archive generation is staged, so failures can still produce a normal API error.
func handleArchiveErr(w http.ResponseWriter, err error) error {
	if errors.Is(err, core.ErrArchiveDelivery) {
		panic(http.ErrAbortHandler)
	}
	if err != nil {
		w.Header().Del("Content-Disposition")
		w.Header().Del("Content-Type")
	}
	return err
}

// Original downloads preserve the physical album image. Playback and converted
// downloads still use virtual track boundaries through the media streamer.
func singleCUESource(tracks model.MediaFiles) *model.MediaFile {
	if len(tracks) == 0 {
		return nil
	}
	for _, track := range tracks {
		if track.CueTrack == 0 || track.AbsolutePath() != tracks[0].AbsolutePath() {
			return nil
		}
	}
	return &tracks[0]
}

func serveOriginalCUE(w http.ResponseWriter, r *http.Request, mf *model.MediaFile) error {
	f, err := os.Open(mf.AbsolutePath())
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	name := filepath.Base(mf.Path)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	if contentType := mime.TypeByExtension(filepath.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, name, info.ModTime(), f)
	return nil
}
