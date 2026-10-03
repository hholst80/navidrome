package stream

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/navidrome/navidrome/core/ffmpeg"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/tests"
	"github.com/navidrome/navidrome/utils/pcmwave"
	"github.com/stretchr/testify/require"
)

func cueWAVFixture(t *testing.T) (model.MediaFile, []byte) {
	t.Helper()
	const rate = 44100
	pcm := make([]byte, 3*rate*6)
	for i := range pcm {
		pcm[i] = byte(i*23 + i/127)
	}
	var b bytes.Buffer
	b.WriteString("RIFF\x00\x00\x00\x00WAVEJUNK\x04\x00\x00\x00testfmt \x10\x00\x00\x00")
	for _, v := range []any{uint16(1), uint16(2), uint32(rate), uint32(rate * 6), uint16(6), uint16(24)} {
		require.NoError(t, binary.Write(&b, binary.LittleEndian, v))
	}
	b.WriteString("data")
	require.NoError(t, binary.Write(&b, binary.LittleEndian, uint32(len(pcm))))
	b.Write(pcm)
	binary.LittleEndian.PutUint32(b.Bytes()[4:], uint32(b.Len()-8))
	path := filepath.Join(t.TempDir(), "album.wav")
	require.NoError(t, os.WriteFile(path, b.Bytes(), 0600))
	return model.MediaFile{ID: "cue", Path: path, Suffix: "wav", CueTrack: 2, CueStartSample: 44100 + 17*588, CueEndSample: 3 * 44100,
		SampleRate: rate, Channels: 2, BitDepth: new(24), Duration: 2 - 17.0/75, Title: "Second", TrackNumber: 2}, pcm
}

func TestCUEDirectWAVHasNoTranscoderOrCacheDependency(t *testing.T) {
	mf, pcm := cueWAVFixture(t)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	// Nil dependencies make any accidental cache/transcoder call fail immediately.
	ms := NewMediaStreamer(nil, nil, nil)
	for _, format := range []string{"", "raw", "wav"} {
		t.Run(format, func(t *testing.T) {
			s, err := ms.NewStream(t.Context(), &mf, Request{Format: format})
			require.NoError(t, err)
			require.True(t, s.Seekable())
			require.Equal(t, "Second.wav", s.Name())
			data, err := io.ReadAll(s)
			require.NoError(t, err)
			require.NoError(t, s.Close())
			source, err := pcmwave.Parse(bytes.NewReader(data), int64(len(data)))
			require.NoError(t, err)
			require.Equal(t, int64(len(pcm)/6)-mf.CueStartSample, source.Samples())
			require.Equal(t, pcm[mf.CueStartSample*6:], data[source.DataOffset:source.DataOffset+source.DataSize])
			for _, bounds := range [][2]int{{0, 3}, {int(source.DataOffset) - 3, int(source.DataOffset) + 9}, {len(data) - 7, len(data) - 1}} {
				s, err = ms.NewStream(t.Context(), &mf, Request{Format: format})
				require.NoError(t, err)
				r := httptest.NewRequest(http.MethodGet, "/stream", nil)
				r.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", bounds[0], bounds[1]))
				w := httptest.NewRecorder()
				_, err = s.Serve(t.Context(), w, r)
				require.NoError(t, err)
				require.NoError(t, s.Close())
				require.Equal(t, http.StatusPartialContent, w.Code)
				require.Equal(t, data[bounds[0]:bounds[1]+1], w.Body.Bytes())
				require.Equal(t, fmt.Sprintf("bytes %d-%d/%d", bounds[0], bounds[1], len(data)), w.Header().Get("Content-Range"))
			}
			s, err = ms.NewStream(t.Context(), &mf, Request{Format: format})
			require.NoError(t, err)
			r := httptest.NewRequest(http.MethodHead, "/stream", nil)
			w := httptest.NewRecorder()
			_, err = s.Serve(t.Context(), w, r)
			require.NoError(t, err)
			require.NoError(t, s.Close())
			require.Equal(t, fmt.Sprint(len(data)), w.Header().Get("Content-Length"))
			require.Empty(t, w.Body.Bytes())
		})
	}
	s, err := ms.NewStream(t.Context(), &mf, Request{Format: "raw", Offset: 1, SampleRate: 8000, Channels: 1, BitDepth: 16})
	require.NoError(t, err)
	data, err := io.ReadAll(s)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	parsed, err := pcmwave.Parse(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Equal(t, 24, parsed.Bits)
	require.Equal(t, 2, parsed.Channels)
	require.Equal(t, pcm[(mf.CueStartSample+44100)*6:], data[parsed.DataOffset:])
	for _, offset := range []int{-1, 2} {
		_, err = ms.NewStream(t.Context(), &mf, Request{Offset: offset})
		require.Error(t, err)
	}
	mf.CueEndSample = 0
	s, err = ms.NewStream(t.Context(), &mf, Request{})
	require.NoError(t, err)
	require.NoError(t, s.Close())
	mf.Suffix = "flac"
	_, err = ms.NewStream(t.Context(), &mf, Request{})
	require.Error(t, err)
	mf.Suffix = "ape"
	_, err = ms.NewStream(t.Context(), &mf, Request{})
	require.Error(t, err)
	entries, err := os.ReadDir(filepath.Dir(mf.Path))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

type cueConversionRepo struct{ model.TranscodingRepository }

func (cueConversionRepo) FindByFormat(string) (*model.Transcoding, error) {
	return nil, model.ErrNotFound
}
func TestCUEExplicitConversionStreamsWithoutCache(t *testing.T) {
	tests.Init(t, false)
	mf, pcm := cueWAVFixture(t)
	ds := &tests.MockDataStore{MockedTranscoding: cueConversionRepo{}}
	ms := NewMediaStreamer(ds, ffmpeg.New(), nil)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	s, err := ms.NewStream(t.Context(), &mf, Request{Format: "flac", Offset: 1, BitDepth: 24})
	require.NoError(t, err)
	data, err := io.ReadAll(s)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	require.False(t, s.Seekable())
	require.True(t, bytes.HasPrefix(data, []byte("fLaC")))
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-i", "pipe:0", "-f", "s24le", "-")
	cmd.Stdin = bytes.NewReader(data)
	decoded, err := cmd.Output()
	require.NoError(t, err)
	require.Equal(t, pcm[(mf.CueStartSample+44100)*6:], decoded)
}
