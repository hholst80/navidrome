package stream

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/tests"
	"github.com/navidrome/navidrome/utils/pcmwave"
	"github.com/stretchr/testify/require"
)

func TestCUELosslessSourcesExposeSeekableWAV(t *testing.T) {
	tests.Init(t, false)
	for _, tc := range []struct {
		name                string
		bits, rate, seconds int
	}{
		{"stereo-16", 16, 44100, 3}, {"stereo-24", 24, 96000, 3}, {"late-16", 16, 44100, 12},
	} {
		for _, suffix := range []string{"ape", "flac"} {
			t.Run(tc.name+"/"+suffix, func(t *testing.T) {
				source, err := filepath.Abs("tests/fixtures/cue-ape/" + tc.name + ".ape")
				require.NoError(t, err)
				if suffix == "flac" {
					flac := filepath.Join(t.TempDir(), "source.flac")
					out, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-i", source, "-c:a", "flac", flac).CombinedOutput()
					require.NoError(t, err, string(out))
					source = flac
				}
				original, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-i", source, "-f", fmt.Sprintf("s%dle", tc.bits), "-").Output()
				require.NoError(t, err)
				require.Len(t, original, tc.seconds*tc.rate*2*tc.bits/8)
				t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
				start := int64((tc.seconds-2)*tc.rate + 17*(tc.rate/75))
				mf := model.MediaFile{ID: "cue-lossless", Path: source, Suffix: suffix, CueTrack: 2, CueStartSample: start,
					SampleRate: tc.rate, Channels: 2, BitDepth: new(tc.bits), Duration: 2 - 17.0/75, Title: "Second", TrackNumber: 2}
				// No transcoder/encoder object and no cache: only the on-demand PCM decoder.
				ms := NewMediaStreamer(nil, nil, nil)
				for _, offset := range []int{0, 1} {
					s, err := ms.NewStream(t.Context(), &mf, Request{Format: "raw", Offset: offset})
					require.NoError(t, err)
					require.True(t, s.Seekable())
					require.Equal(t, "Second.wav", s.Name())
					wav, err := io.ReadAll(s)
					require.NoError(t, err)
					parsed, err := pcmwave.Parse(bytes.NewReader(wav), int64(len(wav)))
					require.NoError(t, err)
					expected := original[(start+int64(offset*tc.rate))*int64(2*tc.bits/8):]
					require.Equal(t, expected, wav[parsed.DataOffset:parsed.DataOffset+parsed.DataSize])
					// A client can obtain the exact length with HEAD before fetching audio.
					head, err := ms.NewStream(t.Context(), &mf, Request{Format: "raw", Offset: offset})
					require.NoError(t, err)
					headResponse := httptest.NewRecorder()
					_, err = head.Serve(t.Context(), headResponse, httptest.NewRequest(http.MethodHead, "/stream", nil))
					require.NoError(t, err)
					require.NoError(t, head.Close())
					require.Equal(t, fmt.Sprint(len(wav)), headResponse.Header().Get("Content-Length"))
					require.Equal(t, "bytes", headResponse.Header().Get("Accept-Ranges"))
					require.Empty(t, headResponse.Body.Bytes())
					// Backwards seek on the same stream must restart decoding precisely.
					at := parsed.DataOffset + 11
					_, err = s.Seek(at, io.SeekStart)
					require.NoError(t, err)
					part := make([]byte, 29)
					_, err = io.ReadFull(s, part)
					require.NoError(t, err)
					require.Equal(t, wav[at:at+29], part)
					require.NoError(t, s.Close())
					// Multipart ranges may seek backwards within one HTTP response.
					multi, err := ms.NewStream(t.Context(), &mf, Request{Format: "raw", Offset: offset})
					require.NoError(t, err)
					request := httptest.NewRequest(http.MethodGet, "/stream", nil)
					request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d,%d-%d", parsed.DataOffset+19, parsed.DataOffset+37, parsed.DataOffset+1, parsed.DataOffset+5))
					response := httptest.NewRecorder()
					_, err = multi.Serve(t.Context(), response, request)
					require.NoError(t, err)
					require.NoError(t, multi.Close())
					require.Equal(t, http.StatusPartialContent, response.Code)
					_, params, err := mime.ParseMediaType(response.Header().Get("Content-Type"))
					require.NoError(t, err)
					multipartReader := multipart.NewReader(bytes.NewReader(response.Body.Bytes()), params["boundary"])
					for _, bounds := range [][2]int64{{parsed.DataOffset + 19, parsed.DataOffset + 37}, {parsed.DataOffset + 1, parsed.DataOffset + 5}} {
						part, err := multipartReader.NextPart()
						require.NoError(t, err)
						data, err := io.ReadAll(part)
						require.NoError(t, err)
						require.Equal(t, wav[bounds[0]:bounds[1]+1], data)
					}
					for _, bounds := range [][2]int64{{0, 3}, {parsed.DataOffset - 3, parsed.DataOffset + 11}, {parsed.DataOffset + 11, parsed.DataOffset + 31}, {int64(len(wav) - 13), int64(len(wav) - 1)}} {
						s, err = ms.NewStream(t.Context(), &mf, Request{Format: "raw", Offset: offset})
						require.NoError(t, err)
						r := httptest.NewRequest(http.MethodGet, "/stream", nil)
						r.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", bounds[0], bounds[1]))
						w := httptest.NewRecorder()
						_, err = s.Serve(t.Context(), w, r)
						require.NoError(t, err)
						require.NoError(t, s.Close())
						require.Equal(t, http.StatusPartialContent, w.Code)
						require.Equal(t, wav[bounds[0]:bounds[1]+1], w.Body.Bytes())
					}
				}
				// A bounded first track stops at the next CUE sample, without bleed.
				mf.CueStartSample = 0
				mf.CueEndSample = start
				s, err := ms.NewStream(t.Context(), &mf, Request{})
				require.NoError(t, err)
				wav, err := io.ReadAll(s)
				require.NoError(t, err)
				require.NoError(t, s.Close())
				parsed, err := pcmwave.Parse(bytes.NewReader(wav), int64(len(wav)))
				require.NoError(t, err)
				require.Equal(t, original[:start*int64(2*tc.bits/8)], wav[parsed.DataOffset:parsed.DataOffset+parsed.DataSize])
			})
		}
	}
}

func TestCUEPlaybackDetailsAndLegacyProfilesUseWAV(t *testing.T) {
	for _, suffix := range []string{"wav", "flac", "ape"} {
		mf := model.MediaFile{Suffix: suffix, Codec: suffix, CueTrack: 1, SampleRate: 44100, Channels: 2, BitDepth: new(24), BitRate: 700, Size: 123456}
		details := buildSourceStream(&mf, nil)
		require.Equal(t, "wav", details.Container)
		require.Equal(t, "pcm", details.Codec)
		require.Equal(t, 2116, details.Bitrate)
		require.Zero(t, details.Size)
		profile := buildLegacyClientInfo(&mf, "wav", 0, 0)
		require.Equal(t, []string{"wav"}, profile.DirectPlayProfiles[0].Containers)
		require.Equal(t, []string{"pcm"}, profile.DirectPlayProfiles[0].AudioCodecs)
		require.Equal(t, "wav", OutputFormat(&mf, "raw"))
	}
}

func TestCUEMultichannelFLACPreservesWAVLayout(t *testing.T) {
	tests.Init(t, false)
	source := filepath.Join(t.TempDir(), "surround.flac")
	output, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-f", "lavfi", "-i",
		"aevalsrc=0.1|0.2|-0.1|-0.2|0.3|-0.3:s=48000:d=1", "-c:a", "flac", "-sample_fmt", "s32", source).CombinedOutput()
	require.NoError(t, err, string(output))
	original, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-i", source, "-f", "s24le", "-").Output()
	require.NoError(t, err)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	mf := model.MediaFile{Path: source, Suffix: "flac", CueTrack: 1, CueStartSample: 13 * 640, CueEndSample: 57 * 640,
		SampleRate: 48000, Channels: 6, BitDepth: new(24), Title: "Surround"}
	s, err := NewMediaStreamer(nil, nil, nil).NewStream(t.Context(), &mf, Request{})
	require.NoError(t, err)
	wav, err := io.ReadAll(s)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-i", "pipe:0", "-f", "s24le", "-")
	cmd.Stdin = bytes.NewReader(wav)
	decoded, err := cmd.Output()
	require.NoError(t, err)
	require.Equal(t, original[mf.CueStartSample*18:mf.CueEndSample*18], decoded)
	probe := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-show_entries", "stream=channel_layout", "-of", "csv=p=0", "pipe:0")
	probe.Stdin = bytes.NewReader(wav)
	layout, err := probe.Output()
	require.NoError(t, err)
	require.Equal(t, "5.1\n", string(layout))
}
