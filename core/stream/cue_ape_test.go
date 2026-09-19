package stream

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/conf/configtest"
	"github.com/navidrome/navidrome/core/ffmpeg"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/tests"
	"github.com/stretchr/testify/require"
)

type cueAPETranscodingRepo struct{ model.TranscodingRepository }

func (cueAPETranscodingRepo) FindByFormat(string) (*model.Transcoding, error) {
	return nil, model.ErrNotFound // Exercise the built-in default commands.
}

func TestCUEAPEPlayback(t *testing.T) {
	tests.Init(t, false)
	for _, tc := range []struct{ bits, rate int }{{16, 44100}, {24, 96000}} {
		t.Run(fmt.Sprint(tc.bits), func(t *testing.T) {
			t.Cleanup(configtest.SetupConfig())
			ctx := t.Context()
			dir := t.TempDir()
			conf.Server.CacheFolder = conf.NewDir(filepath.Join(dir, "cache"))
			conf.Server.TranscodingCacheSize = "10MB"
			cache := NewTranscodingCache()
			require.Eventually(t, func() bool { return cache.Available(ctx) }, 10*time.Second, 10*time.Millisecond)
			ds := &tests.MockDataStore{MockedTranscoding: cueAPETranscodingRepo{}}
			streamer := NewMediaStreamer(ds, ffmpeg.New(), cache)
			source, err := filepath.Abs(fmt.Sprintf("tests/fixtures/cue-ape/stereo-%d.ape", tc.bits))
			require.NoError(t, err)
			boundary := int64(92 * (tc.rate / 75))
			mf := model.MediaFile{ID: "ape-first", Path: source, Suffix: "ape", Title: "First", CueTrack: 1,
				CueEndSample: boundary, SampleRate: tc.rate, Channels: 2, BitDepth: &tc.bits, Duration: 92.0 / 75}
			// A raw request must preserve the source precision even if constraints are supplied.
			s, err := streamer.NewStream(ctx, &mf, Request{Format: "raw", SampleRate: 8000, Channels: 1, BitDepth: 16})
			require.NoError(t, err)
			require.Equal(t, "First.flac", s.Name())
			require.Equal(t, mime.TypeByExtension(".flac"), s.ContentType())
			data, err := io.ReadAll(s)
			require.NoError(t, err)
			require.NoError(t, s.Close())
			require.True(t, bytes.HasPrefix(data, []byte("fLaC")))
			output := filepath.Join(dir, "track.flac")
			require.NoError(t, os.WriteFile(output, data, 0600))
			decode := func(path string) []byte {
				pcm, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", path,
					"-f", fmt.Sprintf("s%dle", tc.bits), "-").Output()
				require.NoError(t, err)
				return pcm
			}
			require.Equal(t, decode(source)[:int(boundary)*2*(tc.bits/8)], decode(output))

			// Cached playback serves ranges of the FLAC track, never of the APE image.
			require.Eventually(t, func() bool {
				s, err = streamer.NewStream(ctx, &mf, Request{})
				if err != nil {
					return false
				}
				if s.Seekable() {
					return true
				}
				_ = s.Close()
				return false
			}, 5*time.Second, 10*time.Millisecond)
			r := httptest.NewRequest(http.MethodGet, "/stream", nil)
			r.Header.Set("Range", "bytes=0-3")
			w := httptest.NewRecorder()
			_, err = s.Serve(ctx, w, r)
			require.NoError(t, err)
			require.NoError(t, s.Close())
			require.Equal(t, http.StatusPartialContent, w.Code)
			require.Equal(t, "fLaC", w.Body.String())
			require.Equal(t, mime.TypeByExtension(".flac"), w.Header().Get("Content-Type"))

			// Ordinary APE tracks still support unchanged raw playback.
			mf.CueTrack = 0
			s, err = streamer.NewStream(ctx, &mf, Request{Format: "raw"})
			require.NoError(t, err)
			original, err := os.ReadFile(source)
			require.NoError(t, err)
			data, err = io.ReadAll(s)
			require.NoError(t, err)
			require.NoError(t, s.Close())
			require.Equal(t, original, data)
			require.Equal(t, "First.ape", s.Name())
		})
	}
}
