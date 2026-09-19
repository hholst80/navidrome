package stream

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/navidrome/navidrome/model"
	"github.com/stretchr/testify/require"
)

func TestUncachedStreamEstimatedLength(t *testing.T) {
	// A real HTTP writer enforces Content-Length; ResponseRecorder does not.
	payload := bytes.Repeat([]byte("audio"), 4096)
	for _, tc := range []struct {
		name, format string
		cue, bitrate int
	}{
		{"WAV CUE", "wav", 1, 0},
		{"FLAC CUE", "flac", 1, 0},
		{"APE CUE FLAC output", "flac", 2, 0},
		{"CUE with requested bitrate", "mp3", 1, 128},
		{"ordinary lossless", "flac", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mf := &model.MediaFile{Title: "Track", Duration: 1, CueTrack: tc.cue}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s := NewStream(mf, tc.format, tc.bitrate, io.NopCloser(bytes.NewReader(payload)))
				defer s.Close()
				_, _ = s.Serve(r.Context(), w, r)
			}))
			defer server.Close()
			resp, err := server.Client().Get(server.URL + "?estimateContentLength=true")
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Empty(t, resp.Header.Get("Content-Length"))
			require.Equal(t, payload, body)
		})
	}
}

func TestCachedCUEStreamExactLengthAndRange(t *testing.T) {
	payload := bytes.Repeat([]byte("audio"), 4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader := bytes.NewReader(payload)
		s := NewStream(&model.MediaFile{Title: "Track", CueTrack: 1}, "flac", 0, io.NopCloser(reader))
		s.Seeker = reader
		defer s.Close()
		_, _ = s.Serve(r.Context(), w, r)
	}))
	defer server.Close()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req, err := http.NewRequest(method, server.URL+"?estimateContentLength=true", nil)
		require.NoError(t, err)
		resp, err := server.Client().Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.EqualValues(t, len(payload), resp.ContentLength)
		if method == http.MethodGet {
			require.Equal(t, payload, body)
		} else {
			require.Empty(t, body)
		}
	}
	req, err := http.NewRequest(http.MethodGet, server.URL+"?estimateContentLength=true", nil)
	require.NoError(t, err)
	req.Header.Set("Range", "bytes=7-23")
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)
	require.EqualValues(t, 17, resp.ContentLength)
	require.Equal(t, payload[7:24], body)
}

func TestOrdinaryStreamLengthEstimateRemainsOptIn(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 16*1024)
	for _, query := range []string{"", "?estimateContentLength=false", "?estimateContentLength=true"} {
		t.Run(query, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s := NewStream(&model.MediaFile{Duration: 1}, "mp3", 128, io.NopCloser(bytes.NewReader(payload)))
				defer s.Close()
				_, _ = s.Serve(r.Context(), w, r)
			}))
			defer server.Close()
			resp, err := server.Client().Get(server.URL + query)
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, payload, body)
			if query == "?estimateContentLength=true" {
				require.EqualValues(t, len(payload), resp.ContentLength)
			} else {
				require.Empty(t, resp.Header.Get("Content-Length"))
			}
		})
	}
}
