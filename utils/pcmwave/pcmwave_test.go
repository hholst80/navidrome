package pcmwave

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixture(bits, channels int, extensible bool) ([]byte, []byte) {
	const samples = 4800
	pcm := make([]byte, samples*channels*bits/8)
	for i := range pcm {
		pcm[i] = byte(i*37 + i/251)
	}
	f := make([]byte, 16)
	binary.LittleEndian.PutUint16(f, 1)
	binary.LittleEndian.PutUint16(f[2:], uint16(channels))
	binary.LittleEndian.PutUint32(f[4:], 48000)
	binary.LittleEndian.PutUint32(f[8:], uint32(48000*channels*bits/8))
	binary.LittleEndian.PutUint16(f[12:], uint16(channels*bits/8))
	binary.LittleEndian.PutUint16(f[14:], uint16(bits))
	if extensible {
		binary.LittleEndian.PutUint16(f, 0xfffe)
		f = append(f, 22, 0, byte(bits), 0, 3, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71)
	}
	var b bytes.Buffer
	b.WriteString("RIFF\x00\x00\x00\x00WAVE")
	writeChunk(&b, "JUNK", []byte{1, 2, 3})
	writeChunk(&b, "fmt ", f)
	writeChunk(&b, "LIST", []byte("INFOINAM\x06\x00\x00\x00Album\x00"))
	writeChunk(&b, "data", pcm)
	binary.LittleEndian.PutUint32(b.Bytes()[4:], uint32(b.Len()-8))
	return b.Bytes(), pcm
}

func TestSampleWindowsAndRanges(t *testing.T) {
	for _, bits := range []int{8, 16, 24, 32} {
		for _, ext := range []bool{false, true} {
			data, pcm := fixture(bits, 2, ext)
			src, err := Parse(bytes.NewReader(data), int64(len(data)))
			require.NoError(t, err)
			out, err := src.Segment(bytes.NewReader(data), 641, 2123, [][2]string{{"INAM", "Track"}})
			require.NoError(t, err)
			result, err := io.ReadAll(out)
			require.NoError(t, err)
			require.Equal(t, "RIFF", string(result[:4]))
			require.Equal(t, len(result)-8, int(binary.LittleEndian.Uint32(result[4:])))
			dataAt := bytes.Index(result, []byte("data")) + 8
			require.Positive(t, dataAt)
			require.Equal(t, pcm[641*2*bits/8:2123*2*bits/8], result[dataAt:])
			require.NotContains(t, string(result[:dataAt]), "Album")
			// Read across the generated header/source boundary, then seek backwards.
			_, err = out.Seek(int64(dataAt-5), io.SeekStart)
			require.NoError(t, err)
			got := make([]byte, 23)
			_, err = io.ReadFull(out, got)
			require.NoError(t, err)
			require.Equal(t, result[dataAt-5:dataAt+18], got)
			_, err = out.Seek(-17, io.SeekEnd)
			require.NoError(t, err)
			got, err = io.ReadAll(out)
			require.NoError(t, err)
			require.Equal(t, result[len(result)-17:], got)
			last, err := src.Segment(bytes.NewReader(data), 4799, 0, nil)
			require.NoError(t, err)
			got, err = io.ReadAll(last)
			require.NoError(t, err)
			require.Equal(t, pcm[len(pcm)-2*bits/8:], got[len(got)-2*bits/8:])
		}
	}
}

func TestRejectUnsupportedAndMalformedWAV(t *testing.T) {
	valid, _ := fixture(16, 2, false)
	fmtAt := bytes.Index(valid, []byte("fmt ")) + 8
	dataAt := bytes.Index(valid, []byte("data"))
	for _, mutate := range []func([]byte){
		func(b []byte) { copy(b, "fLaC") },
		func(b []byte) { binary.LittleEndian.PutUint16(b[fmtAt:], 3) }, // float
		func(b []byte) { binary.LittleEndian.PutUint16(b[fmtAt:], 2) }, // ADPCM
		func(b []byte) { binary.LittleEndian.PutUint16(b[fmtAt+12:], 3) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[dataAt+4:], 0xffffffff) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[dataAt+4:], 1) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[4:], uint32(len(b))) },
	} {
		b := bytes.Clone(valid)
		mutate(b)
		_, err := Parse(bytes.NewReader(b), int64(len(b)))
		require.Error(t, err)
	}
	src, err := Parse(bytes.NewReader(valid), int64(len(valid)))
	require.NoError(t, err)
	for _, bounds := range [][2]int64{{-1, 1}, {2, 1}, {4800, 0}, {0, 4801}} {
		_, err = src.Segment(bytes.NewReader(valid), bounds[0], bounds[1], nil)
		require.Error(t, err)
	}
}

func TestOddPCMDataPadding(t *testing.T) {
	data, pcm := fixture(8, 1, false)
	src, err := Parse(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	r, err := src.Segment(bytes.NewReader(data), 1, 4, nil)
	require.NoError(t, err)
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	at := bytes.Index(b, []byte("data"))
	require.Equal(t, uint32(3), binary.LittleEndian.Uint32(b[at+4:]))
	require.Equal(t, append(bytes.Clone(pcm[1:4]), 0), b[at+8:])
}

type sparseAudio struct {
	header []byte
	reads  int
}

func (s *sparseAudio) ReadAt(p []byte, off int64) (int, error) {
	s.reads++
	clear(p)
	if off < int64(len(s.header)) {
		copy(p, s.header[off:])
	}
	return len(p), nil
}

func TestRF64WithoutReadingOrAllocatingAudio(t *testing.T) {
	data, _ := fixture(24, 2, true)
	// A virtual 6 GB source; constructing/reading headers must not touch PCM.
	src := &Source{Rate: 48000, Channels: 2, Bits: 24, BlockAlign: 6, DataOffset: 4096, DataSize: 6_000_000_000}
	fmtAt := bytes.Index(data, []byte("fmt "))
	src.format = bytes.Clone(data[fmtAt+8 : fmtAt+48])
	sparse := &sparseAudio{}
	r, err := src.Segment(sparse, 0, 0, nil)
	require.NoError(t, err)
	require.Zero(t, sparse.reads)
	header := make([]byte, 104)
	_, err = r.ReadAt(header, 0)
	require.NoError(t, err)
	require.Zero(t, sparse.reads)
	require.Equal(t, "RF64", string(header[:4]))
	require.Equal(t, "ds64", string(header[12:16]))
	require.Equal(t, uint64(src.DataSize), binary.LittleEndian.Uint64(header[28:]))
	// Parse the produced large-file header through a sparse source.
	sparse.header = header
	parsed, err := Parse(sparse, r.Size())
	require.NoError(t, err)
	require.Equal(t, src.Samples(), parsed.Samples())
}
