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
	DescribeTable("names APE CUE playlist and share entries after the streamed format", func(kind, format string) {
		DeferCleanup(configtest.SetupConfig())
		ctx := GinkgoT().Context()
		conf.Server.CacheFolder = conf.NewDir(GinkgoT().TempDir())
		conf.Server.TranscodingCacheSize = "10MB"
		name, err := filepath.Abs("tests/fixtures/cue-ape/stereo-24.ape")
		Expect(err).NotTo(HaveOccurred())
		original, err := os.ReadFile(name)
		Expect(err).NotTo(HaveOccurred())
		tracks := model.MediaFiles{
			{ID: "original", Path: name, Suffix: "ape", Artist: "Artist", Title: "Image"},
			{ID: "first", Path: name, Suffix: "ape", Artist: "Artist", Title: "First", CueTrack: 1,
				CueEndSample: 117760, SampleRate: 96000, Channels: 2, BitDepth: new(24), Duration: 92.0 / 75},
			{ID: "second", Path: name, Suffix: "ape", Artist: "Artist", Title: "Second", CueTrack: 2,
				CueStartSample: 117760, SampleRate: 96000, Channels: 2, BitDepth: new(24), Duration: 3 - 92.0/75},
		}
		ds := &mockDataStore{DataStore: &tests.MockDataStore{MockedTranscoding: cueArchiveTranscodingRepo{}}}
		cache := stream.NewTranscodingCache()
		Eventually(func() bool { return cache.Available(ctx) }, 10*time.Second).Should(BeTrue())
		arch := core.NewArchiver(stream.NewMediaStreamer(ds, ffmpeg.New(), cache), ds, nil)
		var out bytes.Buffer
		if kind == "playlist" {
			repo := &mockPlaylistRepository{}
			repo.On("GetWithTracks", "list", true, false).Return(&model.Playlist{ID: "list", Name: "List",
				Tracks: []model.PlaylistTrack{{MediaFile: tracks[0]}, {MediaFile: tracks[1]}, {MediaFile: tracks[2]}}}, nil)
			ds.On("Playlist", mock.Anything).Return(repo)
			Expect(arch.ZipPlaylist(ctx, "list", format, 0, &out)).To(Succeed())
		} else {
			Expect(arch.ZipShare(ctx, &model.Share{ID: "share", Downloadable: true, Format: format, Tracks: tracks}, &out)).To(Succeed())
		}
		zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
		Expect(err).NotTo(HaveOccurred())
		entries := map[string][]byte{}
		for _, entry := range zr.File {
			r, err := entry.Open()
			Expect(err).NotTo(HaveOccurred())
			entries[entry.Name], err = io.ReadAll(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Close()).To(Succeed())
		}
		Expect(entries["01 - Artist - Image.ape"]).To(Equal(original))
		for _, filename := range []string{"02 - Artist - First.flac", "03 - Artist - Second.flac"} {
			Expect(string(entries[filename])).To(HavePrefix("fLaC"))
			if kind == "playlist" {
				Expect(string(entries["List.m3u"])).To(ContainSubstring("\n" + filename + "\n"))
			}
		}
		if kind == "playlist" {
			Expect(entries).To(HaveLen(4))
			Expect(string(entries["List.m3u"])).To(ContainSubstring("\n01 - Artist - Image.ape\n"))
		} else {
			Expect(entries).To(HaveLen(3))
		}
	}, Entry("raw playlist", "playlist", "raw"), Entry("default playlist", "playlist", ""),
		Entry("raw public share", "share", "raw"), Entry("default public share", "share", ""))

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
			channels := 1
			if sourceFormat == "ape" {
				original, err := os.ReadFile("tests/fixtures/cue-ape/stereo-16.ape")
				Expect(err).NotTo(HaveOccurred())
				Expect(os.WriteFile(source, original, 0600)).To(Succeed())
				channels = 2
			} else {
				output, err := exec.CommandContext(ctx, binary, "-v", "error", "-f", "lavfi", "-i",
					`aevalsrc=if(lt(t\,1)\,0.25\,-0.25):s=44100:d=2`, source).CombinedOutput()
				Expect(err).NotTo(HaveOccurred(), string(output))
			}
			tracks := model.MediaFiles{}
			for i := range 2 {
				disc := 1
				if multiDisc {
					disc = i + 1
				}
				tracks = append(tracks, model.MediaFile{ID: fmt.Sprint(i), Path: source, Suffix: sourceFormat,
					Album: "Album/Name", AlbumID: "album", Title: "Same/Title", DiscNumber: disc, TrackNumber: i + 1,
					CueTrack: i + 1, CueStartSample: int64(i * 44100), CueEndSample: int64((i + 1) * 44100),
					SampleRate: 44100, Channels: channels, Duration: 1})
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
			sourcePCM, err := exec.CommandContext(ctx, binary, "-v", "error", "-i", source, "-f", "s16le", "-").Output()
			Expect(err).NotTo(HaveOccurred())
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
				segmentSize := 44100 * channels * 2
				Expect(pcm).To(Equal(sourcePCM[i*segmentSize : (i+1)*segmentSize]))
			}
		},
		Entry("raw FLAC", "raw", "flac", false),
		Entry("default FLAC", "", "flac", false),
		Entry("raw WAV", "raw", "wav", false),
		Entry("converted WAV to FLAC", "flac", "wav", false),
		Entry("converted FLAC", "flac", "flac", false),
		Entry("multiple discs converted", "flac", "flac", true),
		Entry("raw APE", "raw", "ape", false),
		Entry("default APE", "", "ape", false),
		Entry("APE converted to FLAC", "flac", "ape", false),
	)
})
