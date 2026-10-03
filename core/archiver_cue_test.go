package core_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/conf/configtest"
	"github.com/navidrome/navidrome/consts"
	"github.com/navidrome/navidrome/core"
	"github.com/navidrome/navidrome/core/ffmpeg"
	"github.com/navidrome/navidrome/core/stream"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/tests"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
)

type cueArchiveTranscodingRepo struct{ model.TranscodingRepository }

func (cueArchiveTranscodingRepo) FindByFormat(format string) (*model.Transcoding, error) {
	for _, transcoding := range consts.DefaultTranscodings {
		if transcoding.TargetFormat == format {
			return &model.Transcoding{TargetFormat: format, Command: transcoding.Command}, nil
		}
	}
	return nil, model.ErrNotFound
}

var _ = Describe("CUE archive downloads", func() {
	It("streams mixed playlist exports with WAV names for APE CUE tracks", func() {
		ctx := GinkgoT().Context()
		source, err := filepath.Abs("tests/fixtures/cue-ape/stereo-24.ape")
		Expect(err).NotTo(HaveOccurred())
		ordinary, err := filepath.Abs("tests/fixtures/test.mp3")
		Expect(err).NotTo(HaveOccurred())
		boundary := int64(92 * (96000 / 75))
		cueTrack := model.MediaFile{ID: "cue", Path: source, Suffix: "ape", CueTrack: 2, CueStartSample: boundary,
			SampleRate: 96000, Channels: 2, BitDepth: new(24), Artist: "Artist", Title: "Second", Duration: 3 - 92.0/75}
		ordinaryTrack := model.MediaFile{ID: "ordinary", Path: ordinary, Suffix: "mp3", Artist: "Other", Title: "Song"}
		repo := &mockPlaylistRepository{}
		repo.On("GetWithTracks", "playlist", true, false).Return(&model.Playlist{ID: "playlist", Name: "Mixed",
			Tracks: []model.PlaylistTrack{{MediaFile: cueTrack}, {MediaFile: ordinaryTrack}}}, nil)
		ds := &mockDataStore{DataStore: &tests.MockDataStore{}}
		ds.On("Playlist", mock.Anything).Return(repo)
		arch := core.NewArchiver(stream.NewMediaStreamer(ds, nil, nil), ds, nil)
		GinkgoT().Setenv("TMPDIR", filepath.Join(GinkgoT().TempDir(), "does-not-exist"))
		var out bytes.Buffer
		Expect(arch.ZipPlaylist(ctx, "playlist", "raw", 0, &out)).To(Succeed())
		zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
		Expect(err).NotTo(HaveOccurred())
		Expect(zr.File).To(HaveLen(3))
		Expect(zr.File[0].Name).To(Equal("01 - Artist - Second.wav"))
		Expect(zr.File[1].Name).To(Equal("02 - Other - Song.mp3"))
		read := func(entry *zip.File) []byte {
			r, err := entry.Open()
			Expect(err).NotTo(HaveOccurred())
			data, err := io.ReadAll(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Close()).To(Succeed())
			return data
		}
		wav := read(zr.File[0])
		command := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", "pipe:0", "-f", "s24le", "-")
		command.Stdin = bytes.NewReader(wav)
		decoded, err := command.Output()
		Expect(err).NotTo(HaveOccurred())
		original, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", source, "-f", "s24le", "-").Output()
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(Equal(original[boundary*6:]))
		originalMP3, err := os.ReadFile(ordinary)
		Expect(err).NotTo(HaveOccurred())
		Expect(read(zr.File[1])).To(Equal(originalMP3))
		playlist := string(read(zr.File[2]))
		Expect(playlist).To(ContainSubstring(zr.File[0].Name))
		Expect(playlist).To(ContainSubstring(zr.File[1].Name))
	})

	DescribeTable("exports distinct audio segments with unique names",
		func(format, sourceFormat string, multiDisc bool) {
			DeferCleanup(configtest.SetupConfig())
			ctx := context.Background()
			dir := GinkgoT().TempDir()
			conf.Server.CacheFolder = conf.NewDir(filepath.Join(dir, "cache"))
			conf.Server.TranscodingCacheSize = "10MB"
			source := filepath.Join(dir, "album."+sourceFormat)
			binary, err := exec.LookPath("ffmpeg")
			Expect(err).NotTo(HaveOccurred())
			output, err := exec.CommandContext(ctx, binary, "-v", "error", "-f", "lavfi", "-i",
				`aevalsrc=if(lt(t\,1)\,0.25\,-0.25):s=44100:d=2`, source).CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(output))
			tracks := model.MediaFiles{}
			for i := range 2 {
				disc := 1
				if multiDisc {
					disc = i + 1
				}
				tracks = append(tracks, model.MediaFile{ID: fmt.Sprint(i), Path: source, Suffix: sourceFormat,
					Album: "Album/Name", AlbumID: "album", Title: "Same/Title", DiscNumber: disc, TrackNumber: i + 1,
					CueTrack: i + 1, CueStartSample: int64(i * 44100), CueEndSample: int64((i + 1) * 44100),
					SampleRate: 44100, Channels: 1, Duration: 1})
			}
			repo := &mockMediaFileRepository{}
			repo.On("GetAll", mock.Anything).Return(tracks, nil)
			ds := &mockDataStore{DataStore: &tests.MockDataStore{MockedTranscoding: cueArchiveTranscodingRepo{}}}
			ds.On("MediaFile", mock.Anything).Return(repo)
			cache := stream.NewTranscodingCache()
			Eventually(func() bool { return cache.Available(ctx) }, 10*time.Second).Should(BeTrue())
			arch := core.NewArchiver(stream.NewMediaStreamer(ds, ffmpeg.New(), cache), ds, nil)
			var out bytes.Buffer
			Expect(arch.ZipAlbum(ctx, "album", format, 0, &out)).To(Succeed())
			zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
			Expect(err).NotTo(HaveOccurred())
			if format == "raw" || format == "" {
				Expect(zr.File).To(HaveLen(1))
				r, err := zr.File[0].Open()
				Expect(err).NotTo(HaveOccurred())
				data, err := io.ReadAll(r)
				Expect(err).NotTo(HaveOccurred())
				Expect(r.Close()).To(Succeed())
				original, err := os.ReadFile(source)
				Expect(err).NotTo(HaveOccurred())
				Expect(data).To(Equal(original))
				Expect(zr.File[0].Name).To(HaveSuffix("/album." + sourceFormat))
				return
			}
			Expect(zr.File).To(HaveLen(2))
			ext := sourceFormat
			if format != "" && format != "raw" {
				ext = format
			}
			for i, entry := range zr.File {
				prefix := "Album_Name/"
				if multiDisc {
					prefix += fmt.Sprintf("Disc %02d/", i+1)
				}
				Expect(entry.Name).To(Equal(fmt.Sprintf("%s%02d - Same_Title.%s", prefix, i+1, ext)))
				r, err := entry.Open()
				Expect(err).NotTo(HaveOccurred())
				data, err := io.ReadAll(r)
				Expect(err).NotTo(HaveOccurred())
				Expect(r.Close()).To(Succeed())
				file := filepath.Join(dir, fmt.Sprintf("track-%d.%s", i, ext))
				Expect(os.WriteFile(file, data, 0o600)).To(Succeed())
				pcm, err := exec.CommandContext(ctx, binary, "-v", "error", "-i", file, "-f", "s16le", "-").Output()
				Expect(err).NotTo(HaveOccurred())
				Expect(pcm).To(HaveLen(44100 * 2))
				// The first second is positive DC, the second negative: verify content as well as length.
				Expect(pcm[1] < 128).To(Equal(i == 0))
			}
		},

		Entry("default WAV", "", "wav", false),
		Entry("raw FLAC", "raw", "flac", false),
		Entry("FLAC to WAV", "wav", "flac", false),
		Entry("raw WAV", "raw", "wav", false),
		Entry("converted WAV to FLAC", "flac", "wav", false),
		Entry("split WAV", "wav", "wav", false),
		Entry("multiple discs converted", "flac", "wav", true),
	)
})
