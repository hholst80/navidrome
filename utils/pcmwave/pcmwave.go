// Package pcmwave exposes sample-exact windows of PCM WAV files without copying
// or decoding audio. Only the generated container header is held in memory.
package pcmwave

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/bits"
)

type Source struct {
	Rate       int
	Channels   int
	Bits       int
	BlockAlign int64
	DataOffset int64
	DataSize   int64
	format     []byte
}

func (s *Source) Samples() int64 { return s.DataSize / s.BlockAlign }

// New describes a decoded PCM stream. Its ReaderAt starts at PCM byte zero.
func New(rate, channels, bitDepth int, samples int64, mask uint32) (*Source, error) {
	if samples <= 0 || channels <= 0 || channels > 8 || rate <= 0 ||
		(bitDepth != 8 && bitDepth != 16 && bitDepth != 24 && bitDepth != 32) ||
		(mask != 0 && bits.OnesCount32(mask) != channels) {
		return nil, fmt.Errorf("unsupported decoded PCM format")
	}
	align := int64(channels * bitDepth / 8)
	if samples > (math.MaxInt64-(1<<21))/align || uint64(rate)*uint64(align) > math.MaxUint32 {
		return nil, fmt.Errorf("decoded PCM source is too large")
	}
	f := make([]byte, 16)
	binary.LittleEndian.PutUint16(f, 1)
	binary.LittleEndian.PutUint16(f[2:], uint16(channels))
	binary.LittleEndian.PutUint32(f[4:], uint32(rate))
	binary.LittleEndian.PutUint32(f[8:], uint32(int64(rate)*align))
	binary.LittleEndian.PutUint16(f[12:], uint16(align))
	binary.LittleEndian.PutUint16(f[14:], uint16(bitDepth))
	if channels > 2 || bitDepth > 16 {
		binary.LittleEndian.PutUint16(f, 0xfffe)
		f = append(f, 22, 0, byte(bitDepth), 0, 0, 0, 0, 0,
			1, 0, 0, 0, 0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71)
		binary.LittleEndian.PutUint32(f[20:], mask)
	}
	return &Source{Rate: rate, Channels: channels, Bits: bitDepth, BlockAlign: align,
		DataSize: samples * align, format: f}, nil
}

// Parse reads chunk headers, never PCM data. RIFF, RF64 and the integer PCM
// subtype of WAVE_FORMAT_EXTENSIBLE are supported; compressed/float WAV is not.
func Parse(r io.ReaderAt, size int64) (*Source, error) {
	var head [12]byte
	if _, err := r.ReadAt(head[:], 0); err != nil {
		return nil, err
	}
	rf64 := string(head[:4]) == "RF64"
	if (!rf64 && string(head[:4]) != "RIFF") || string(head[8:]) != "WAVE" {
		return nil, fmt.Errorf("CUE source must be a PCM WAV file")
	}
	limit := int64(binary.LittleEndian.Uint32(head[4:])) + 8
	var rfData uint64
	pos := int64(12)
	if rf64 {
		var ds [36]byte
		if _, err := r.ReadAt(ds[:], pos); err != nil {
			return nil, err
		}
		dsSize := int64(binary.LittleEndian.Uint32(ds[4:]))
		riffSize := binary.LittleEndian.Uint64(ds[8:])
		if string(ds[:4]) != "ds64" || dsSize < 28 || riffSize > math.MaxInt64-8 || dsSize > size-20 {
			return nil, fmt.Errorf("invalid RF64 ds64 chunk")
		}
		limit = int64(riffSize) + 8
		rfData = binary.LittleEndian.Uint64(ds[16:])
		pos += 8 + dsSize + dsSize%2
	}
	if limit > size || limit < pos {
		return nil, fmt.Errorf("truncated WAV container")
	}
	s := &Source{}
	for chunks := 0; pos <= limit-8 && chunks < 4096; chunks++ {
		var chunk [8]byte
		if _, err := r.ReadAt(chunk[:], pos); err != nil {
			return nil, err
		}
		n := uint64(binary.LittleEndian.Uint32(chunk[4:]))
		if rf64 && string(chunk[:4]) == "data" && n == math.MaxUint32 {
			n = rfData
		}
		pos += 8
		if n > uint64(limit-pos) || n+n%2 > uint64(limit-pos) {
			return nil, fmt.Errorf("truncated WAV chunk")
		}
		switch string(chunk[:4]) {
		case "fmt ":
			if s.format != nil || n < 16 || n > 4096 {
				return nil, fmt.Errorf("invalid WAV format chunk")
			}
			s.format = make([]byte, int(n))
			if _, err := r.ReadAt(s.format, pos); err != nil {
				return nil, err
			}
			if err := s.validate(); err != nil {
				return nil, err
			}
		case "data":
			if s.BlockAlign == 0 || n%uint64(s.BlockAlign) != 0 || n == 0 {
				return nil, fmt.Errorf("invalid PCM WAV data chunk")
			}
			s.DataOffset, s.DataSize = pos, int64(n)
			return s, nil
		}
		pos += int64(n + n%2)
	}
	return nil, fmt.Errorf("WAV data chunk not found")
}

func (s *Source) validate() error {
	f := s.format
	encoding := binary.LittleEndian.Uint16(f)
	s.Channels = int(binary.LittleEndian.Uint16(f[2:]))
	s.Rate = int(binary.LittleEndian.Uint32(f[4:]))
	s.BlockAlign = int64(binary.LittleEndian.Uint16(f[12:]))
	s.Bits = int(binary.LittleEndian.Uint16(f[14:]))
	if encoding == 0xfffe {
		pcmGUID := []byte{1, 0, 0, 0, 0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}
		if len(f) < 40 || binary.LittleEndian.Uint16(f[16:]) < 22 || int(binary.LittleEndian.Uint16(f[16:]))+18 > len(f) || !bytes.Equal(f[24:40], pcmGUID) ||
			int(binary.LittleEndian.Uint16(f[18:])) > s.Bits {
			return fmt.Errorf("CUE supports only integer PCM WAV")
		}
	} else if encoding != 1 {
		return fmt.Errorf("CUE supports only integer PCM WAV")
	}
	if s.Rate <= 0 || s.Channels <= 0 || (s.Bits != 8 && s.Bits != 16 && s.Bits != 24 && s.Bits != 32) ||
		s.BlockAlign != int64(s.Channels*s.Bits/8) ||
		uint64(binary.LittleEndian.Uint32(f[8:])) != uint64(s.Rate)*uint64(s.BlockAlign) {
		return fmt.Errorf("invalid PCM WAV format")
	}
	return nil
}

// Segment returns a seekable virtual WAV. End is exclusive; zero means EOF.
// INFO tags replace album-level metadata rather than copying it into each track.
func (s *Source) Segment(r io.ReaderAt, start, end int64, tags [][2]string) (*io.SectionReader, error) {
	if end == 0 {
		end = s.Samples()
	}
	if start < 0 || start >= end || end > s.Samples() {
		return nil, fmt.Errorf("CUE sample range is outside the WAV data")
	}
	dataSize := (end - start) * s.BlockAlign
	if dataSize > math.MaxInt64-(1<<21) {
		return nil, fmt.Errorf("WAV segment is too large")
	}
	var info bytes.Buffer
	info.WriteString("INFO")
	for _, tag := range tags {
		if len(tag[0]) != 4 || tag[1] == "" {
			continue
		}
		if len(tag[1]) > 65535 || info.Len()+len(tag[1])+10 > 1<<20 {
			return nil, fmt.Errorf("WAV track metadata too large")
		}
		writeChunk(&info, tag[0], append([]byte(tag[1]), 0))
	}
	var header bytes.Buffer
	header.WriteString("RIFF\x00\x00\x00\x00WAVE")
	writeChunk(&header, "fmt ", s.format)
	if info.Len() > 4 {
		writeChunk(&header, "LIST", info.Bytes())
	}
	header.WriteString("data\xff\xff\xff\xff")
	b := header.Bytes()
	riffSize := int64(len(b)-8) + dataSize + dataSize%2
	if riffSize > math.MaxUint32 {
		ds := make([]byte, 36)
		copy(ds, "ds64")
		binary.LittleEndian.PutUint32(ds[4:], 28)
		binary.LittleEndian.PutUint64(ds[8:], uint64(riffSize+36))
		binary.LittleEndian.PutUint64(ds[16:], uint64(dataSize))
		binary.LittleEndian.PutUint64(ds[24:], uint64(end-start))
		copy(b, "RF64\xff\xff\xff\xff")
		b = append(append(append([]byte{}, b[:12]...), ds...), b[12:]...)
	} else {
		binary.LittleEndian.PutUint32(b[4:], uint32(riffSize))
		binary.LittleEndian.PutUint32(b[len(b)-4:], uint32(dataSize))
	}
	v := &virtualWAV{header: b, audio: io.NewSectionReader(r, s.DataOffset+start*s.BlockAlign, dataSize), dataSize: dataSize}
	return io.NewSectionReader(v, 0, int64(len(b))+dataSize+dataSize%2), nil
}

func writeChunk(w *bytes.Buffer, name string, data []byte) {
	w.WriteString(name)
	_ = binary.Write(w, binary.LittleEndian, uint32(len(data)))
	w.Write(data)
	if len(data)%2 != 0 {
		w.WriteByte(0)
	}
}

type virtualWAV struct {
	header   []byte
	audio    *io.SectionReader
	dataSize int64
}

func (v *virtualWAV) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 {
		return 0, fmt.Errorf("negative WAV read offset")
	}
	if off < int64(len(v.header)) {
		k := copy(p, v.header[off:])
		n, off, p = k, off+int64(k), p[k:]
	}
	pos := off - int64(len(v.header))
	if len(p) > 0 && pos < v.dataSize {
		want := min(int64(len(p)), v.dataSize-pos)
		k, err := v.audio.ReadAt(p[:want], pos)
		n, pos, p = n+k, pos+int64(k), p[k:]
		if err != nil {
			return n, err
		}
	}
	if len(p) > 0 && pos == v.dataSize && v.dataSize%2 != 0 {
		p[0] = 0
		n, p = n+1, p[1:]
	}
	if len(p) > 0 {
		err = io.EOF
	}
	return n, err
}
