package ffmpeg

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func pcmFixture(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "../../tests/fixtures/cue-ape/late-16.ape")
}

func TestPCMReaderReusesDecoderAndReleasesOnSeekAndClose(t *testing.T) {
	path := pcmFixture(t)
	source, err := ProbePCM(t.Context(), path)
	require.NoError(t, err)
	acquired, released := 0, 0
	acquire := func(context.Context) (func(), error) { acquired++; return func() { released++ }, nil }
	r := NewPCMReader(t.Context(), path, source, 0, acquire)
	require.Zero(t, acquired)
	p := make([]byte, 29)
	off := int64(10*44100*4 + 7)
	n, err := r.ReadAt(p, off)
	require.NoError(t, err)
	require.Equal(t, len(p), n)
	require.Equal(t, 1, acquired)
	n, err = r.ReadAt(p, off+int64(len(p)))
	require.NoError(t, err)
	require.Equal(t, len(p), n)
	require.Equal(t, 1, acquired)
	_, err = r.ReadAt(p, 4)
	require.NoError(t, err)
	require.Equal(t, 2, acquired)
	require.Equal(t, 1, released)
	require.NoError(t, r.Close())
	require.Equal(t, 2, released)
	_, err = r.ReadAt(p, 0)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, r.Close())
	require.Equal(t, 2, released)
}

func TestPCMReaderPropagatesDecoderFailuresAndLimits(t *testing.T) {
	source, err := ProbePCM(t.Context(), pcmFixture(t))
	require.NoError(t, err)
	released := 0
	r := NewPCMReader(t.Context(), "/does-not-exist.ape", source, 0, func(context.Context) (func(), error) { return func() { released++ }, nil })
	_, err = r.ReadAt(make([]byte, 16), 0)
	require.Error(t, err)
	require.NotErrorIs(t, err, io.EOF)
	require.Equal(t, 1, released)
	require.NoError(t, r.Close())
	busy := errors.New("busy")
	r = NewPCMReader(t.Context(), pcmFixture(t), source, 0, func(context.Context) (func(), error) { return nil, busy })
	_, err = r.ReadAt(make([]byte, 16), 0)
	require.ErrorIs(t, err, busy)
	require.NoError(t, r.Close())
}

func TestVirtualWAVLengthsAreAvailableBeforeDecoding(t *testing.T) {
	path := pcmFixture(t)
	source, err := ProbePCM(t.Context(), path)
	require.NoError(t, err)
	start := int64(10*44100 + 17*(44100/75))
	end := int64(11*44100 + 13*(44100/75))
	acquired := 0
	decoder := NewPCMReader(t.Context(), path, source, end, func(context.Context) (func(), error) { acquired++; return func() {}, nil })
	defer decoder.Close()
	wav, err := source.Segment(decoder, start, end, nil)
	require.NoError(t, err)
	header := make([]byte, 44)
	_, err = io.ReadFull(wav, header)
	require.NoError(t, err)
	payload := (end - start) * source.BlockAlign
	require.Equal(t, uint32(payload), binary.LittleEndian.Uint32(header[40:]))
	require.Equal(t, uint32(payload+36), binary.LittleEndian.Uint32(header[4:]))
	size, err := wav.Seek(0, io.SeekEnd)
	require.NoError(t, err)
	require.Equal(t, payload+44, size)
	require.Zero(t, acquired, "header and total byte length must be known without decoding any audio")
	_, err = wav.Seek(size-7, io.SeekStart)
	require.NoError(t, err)
	tail := make([]byte, 7)
	_, err = io.ReadFull(wav, tail)
	require.NoError(t, err)
	require.Equal(t, 1, acquired)
}
