package scanner_test

import (
	"encoding/binary"
	"encoding/json"
	"github.com/navidrome/navidrome/core/storage"
	"io"
	"io/fs"
	"net/url"
	"strings"
	"testing/fstest"

	"github.com/navidrome/navidrome/core/storage/storagetest"
)

// Keep fake tag extraction while exposing real WAV headers and sparse PCM to
// the scanner's independent format validation.
type cuePCMFS struct{ *storagetest.FakeFS }

func createCUEFS(files fstest.MapFS) storagetest.FakeFS {
	base := createFS(files)
	// FakeStorage is concrete, so register the wrapper through the storage API.
	registerCUEFS(&cuePCMFS{&base})
	return base
}
func (f *cuePCMFS) Open(name string) (fs.File, error) {
	original, err := f.FakeFS.Open(name)
	if err != nil || !strings.HasSuffix(name, ".wav") {
		return original, err
	}
	defer original.Close()
	var tags map[string]any
	if err = json.NewDecoder(original).Decode(&tags); err != nil {
		return nil, err
	}
	rate, seconds := int(tags["samplerate"].(float64)), int(tags["duration"].(float64))
	align := 4
	dataSize := int64(rate * seconds * align)
	head := make([]byte, 44)
	copy(head, "RIFF")
	binary.LittleEndian.PutUint32(head[4:], uint32(dataSize+36))
	copy(head[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(head[16:], 16)
	binary.LittleEndian.PutUint16(head[20:], 1)
	binary.LittleEndian.PutUint16(head[22:], 2)
	binary.LittleEndian.PutUint32(head[24:], uint32(rate))
	binary.LittleEndian.PutUint32(head[28:], uint32(rate*align))
	binary.LittleEndian.PutUint16(head[32:], uint16(align))
	binary.LittleEndian.PutUint16(head[34:], 16)
	copy(head[36:], "data")
	binary.LittleEndian.PutUint32(head[40:], uint32(dataSize))
	if encoding, ok := tags["wav_format"].(float64); ok {
		binary.LittleEndian.PutUint16(head[20:], uint16(encoding))
	}
	info, err := original.Stat()
	if err != nil {
		return nil, err
	}
	return &cuePCMFile{SectionReader: io.NewSectionReader(cuePCMData(head), 0, 44+dataSize), info: cuePCMInfo{info, 44 + dataSize}}, nil
}

type cuePCMData []byte

func (h cuePCMData) ReadAt(p []byte, off int64) (int, error) {
	clear(p)
	if off < int64(len(h)) {
		copy(p, h[off:])
	}
	return len(p), nil
}

type cuePCMInfo struct {
	fs.FileInfo
	size int64
}

func (f cuePCMInfo) Size() int64 { return f.size }

type cuePCMFile struct {
	*io.SectionReader
	info fs.FileInfo
}

func (f *cuePCMFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *cuePCMFile) Close() error               { return nil }

type cuePCMStorage struct{ fs *cuePCMFS }

func (s cuePCMStorage) FS() (storage.MusicFS, error) { return s.fs, nil }
func registerCUEFS(f *cuePCMFS) {
	storage.Register("fake", func(url.URL) storage.Storage { return cuePCMStorage{f} })
}
