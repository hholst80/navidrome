package subsonic

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/Masterminds/squirrel"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/conf/configtest"
	"github.com/navidrome/navidrome/core"
	"github.com/navidrome/navidrome/core/ffmpeg"
	"github.com/navidrome/navidrome/core/stream"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/tests"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type cueDownloadArchiver struct {
	core.Archiver
	format string
}

type cueDownloadTranscodingRepo struct{ model.TranscodingRepository }

func (cueDownloadTranscodingRepo) FindByFormat(string) (*model.Transcoding, error) {
	return nil, model.ErrNotFound // Exercise the built-in default commands.
}

func (a *cueDownloadArchiver) ZipAlbum(_ context.Context, _ string, format string, _ int, w io.Writer) error {
	a.format = format
	_, err := io.WriteString(w, "converted album archive")
	return err
}

var _ = Describe("CUE original downloads", func() {
	DescribeTable("distinguishes original files from converted tracks", func(id, format, sourceFormat string, original bool) {
		DeferCleanup(configtest.SetupConfig())
		conf.Server.EnableDownloads = true
		conf.Server.AutoTranscodeDownload = false
		name := filepath.Join(GinkgoT().TempDir(), "original album."+sourceFormat)
		source := []byte("fLaC unchanged complete album image")
		if sourceFormat == "ape" {
			var err error
			source, err = os.ReadFile("tests/fixtures/cue-ape/stereo-16.ape")
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(os.WriteFile(name, source, 0600)).To(Succeed())
		repo := &tests.MockMediaFileRepo{}
		repo.SetData(model.MediaFiles{{ID: "track1", AlbumID: "album", Path: name, CueTrack: 1}, {ID: "track2", AlbumID: "album", Path: name, CueTrack: 2}})
		albums := tests.CreateMockAlbumRepo()
		albums.SetData(model.Albums{{ID: "album", Name: "Album"}})
		ds := &tests.MockDataStore{MockedMediaFile: repo, MockedAlbum: albums}
		archiver := &cueDownloadArchiver{}
		router := &Router{ds: ds, archiver: archiver}
		w := httptest.NewRecorder()
		_, err := router.Download(w, newGetRequest("id="+id+"&format="+format))
		Expect(err).NotTo(HaveOccurred())
		if original {
			Expect(w.Body.Bytes()).To(Equal(source))
			_, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
			Expect(err).NotTo(HaveOccurred())
			Expect(params["filename"]).To(Equal("original album." + sourceFormat))
			Expect(archiver.format).To(BeEmpty())
			if id == "album" {
				Expect(repo.Options.Filters).To(Equal(squirrel.Eq{"album_id": "album", "missing": false}))
			}
			r := newGetRequest("id=" + id + "&format=" + format)
			r.Header.Set("Range", "bytes=2-12")
			w = httptest.NewRecorder()
			_, err = router.Download(w, r)
			Expect(err).NotTo(HaveOccurred())
			Expect(w.Code).To(Equal(http.StatusPartialContent))
			Expect(w.Body.Bytes()).To(Equal(source[2:13]))
		} else {
			Expect(archiver.format).To(Equal("flac"))
			Expect(w.Header().Get("Content-Type")).To(Equal("application/zip"))
		}
	}, Entry("original album", "album", "raw", "flac", true), Entry("default album", "album", "", "flac", true),
		Entry("original track", "track1", "raw", "flac", true), Entry("converted FLAC album", "album", "flac", "flac", false),
		Entry("original APE album", "album", "raw", "ape", true), Entry("default APE album", "album", "", "ape", true),
		Entry("original APE track", "track1", "raw", "ape", true), Entry("converted APE album", "album", "flac", "ape", false))

	DescribeTable("preserves original APE and CUE hashes and exports clean FLAC splits", func(bits int, id, format, cueSuffix, mode string) {
		DeferCleanup(configtest.SetupConfig())
		conf.Server.EnableDownloads = true
		conf.Server.AutoTranscodeDownload = false
		conf.Server.CacheFolder = conf.NewDir(GinkgoT().TempDir())
		conf.Server.TranscodingCacheSize = "10MB"
		dir := GinkgoT().TempDir()
		base := fmt.Sprintf("stereo-%d", bits)
		original := map[string][]byte{}
		for _, ext := range []string{".ape", ".cue"} {
			data, err := os.ReadFile("tests/fixtures/cue-ape/" + base + ext)
			Expect(err).NotTo(HaveOccurred())
			name := base + ext
			if ext == ".cue" {
				name = base + cueSuffix
			}
			original[name] = data
			Expect(os.WriteFile(filepath.Join(dir, name), data, 0600)).To(Succeed())
		}
		// The canonical sidecar wins over a duplicate .ape.cue.
		if cueSuffix == ".cue" {
			Expect(os.WriteFile(filepath.Join(dir, base+".ape.cue"), original[base+".cue"], 0600)).To(Succeed())
		}
		conf.Server.Scanner.FollowSymlinks = true
		sourcePath := filepath.Join(dir, base+".ape")
		if mode == "case" {
			physical := filepath.Join(dir, base+".APE")
			Expect(os.Rename(sourcePath, physical)).To(Succeed())
			sourcePath = physical
		}
		if mode == "symlink" {
			sheet := filepath.Join(dir, base+cueSuffix)
			target := filepath.Join(GinkgoT().TempDir(), "source.cue")
			Expect(os.Rename(sheet, target)).To(Succeed())
			Expect(os.Symlink(target, sheet)).To(Succeed())
			Expect(os.Symlink(filepath.Join(dir, "missing.cue"), filepath.Join(dir, "000-broken.cue"))).To(Succeed())
		}
		rate := 44100
		if bits == 24 {
			rate = 96000
		}
		boundary := int64(92 * (rate / 75))
		repo := &tests.MockMediaFileRepo{}
		repo.SetData(model.MediaFiles{
			{ID: "track1", AlbumID: "album", Album: "Album", Path: sourcePath, Suffix: "ape", Title: "First", CueTrack: 1, CueEndSample: boundary, SampleRate: rate, Channels: 2, BitDepth: new(bits), Duration: 92.0 / 75},
			{ID: "track2", AlbumID: "album", Album: "Album", Path: sourcePath, Suffix: "ape", Title: "Second", CueTrack: 2, CueStartSample: boundary, SampleRate: rate, Channels: 2, BitDepth: new(bits), Duration: 3 - 92.0/75},
		})
		albums := tests.CreateMockAlbumRepo()
		albums.SetData(model.Albums{{ID: "album", Name: "Album"}})
		ds := &tests.MockDataStore{MockedMediaFile: repo, MockedAlbum: albums, MockedTranscoding: cueDownloadTranscodingRepo{}}
		ff := ffmpeg.New()
		cache := stream.NewTranscodingCache()
		Eventually(func() bool { return cache.Available(GinkgoT().Context()) }, 10*time.Second).Should(BeTrue())
		ms := stream.NewMediaStreamer(ds, ff, cache)
		router := &Router{ds: ds, archiver: core.NewArchiver(ms, ds, nil), streamer: ms, transcodeDecision: stream.NewTranscodeDecider(ds, ff)}
		w := httptest.NewRecorder()
		_, err := router.Download(w, newGetRequest("id="+id+"&format="+format))
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(w.Header().Get("Content-Type")).To(Equal("application/zip"))
		zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		Expect(err).NotTo(HaveOccurred())
		Expect(zr.File).To(HaveLen(2))
		var combinedPCM []byte
		for _, entry := range zr.File {
			r, err := entry.Open()
			Expect(err).NotTo(HaveOccurred())
			data, err := io.ReadAll(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Close()).To(Succeed())
			if format != "flac" {
				Expect(original).To(HaveKey(entry.Name))
				expected, actual := sha256.Sum256(original[entry.Name]), sha256.Sum256(data)
				Expect(actual).To(Equal(expected))
				fmt.Fprintf(GinkgoWriter, "SHA256 original %s %x = downloaded %x\n", entry.Name, expected, actual)
			} else {
				Expect(entry.Name).To(HaveSuffix(".flac"))
				Expect(string(data)).To(HavePrefix("fLaC"))
				output := filepath.Join(dir, filepath.Base(entry.Name))
				Expect(os.WriteFile(output, data, 0600)).To(Succeed())
				tags, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags:stream_tags", "-of", "json", output).Output()
				Expect(err).NotTo(HaveOccurred())
				Expect(strings.ToLower(string(tags))).NotTo(ContainSubstring("cuesheet"))
				pcm, err := exec.Command("ffmpeg", "-v", "error", "-i", output, "-f", fmt.Sprintf("s%dle", bits), "-").Output()
				Expect(err).NotTo(HaveOccurred())
				combinedPCM = append(combinedPCM, pcm...)
			}
		}
		if format == "flac" {
			// #nosec G204 -- The filename and PCM format come from this synthetic test table.
			pcm, err := exec.Command("ffmpeg", "-v", "error", "-i", filepath.Join(dir, base+".ape"), "-f", fmt.Sprintf("s%dle", bits), "-").Output()
			Expect(err).NotTo(HaveOccurred())
			Expect(sha256.Sum256(combinedPCM)).To(Equal(sha256.Sum256(pcm)))
			fmt.Fprintf(GinkgoWriter, "SHA256 %d-bit decoded original PCM %x = concatenated FLAC PCM %x; no CUE files or CUESHEET tags\n", bits, sha256.Sum256(pcm), sha256.Sum256(combinedPCM))
		}
	}, Entry("16-bit raw album", 16, "album", "raw", ".cue", ""), Entry("24-bit default album", 24, "album", "", ".cue", ""),
		Entry("16-bit raw track with .ape.cue", 16, "track1", "raw", ".ape.cue", ""), Entry("24-bit raw track", 24, "track1", "raw", ".cue", ""),
		Entry("16-bit FLAC album", 16, "album", "flac", ".cue", ""), Entry("24-bit FLAC album", 24, "album", "flac", ".cue", ""),
		Entry("case mismatch original album", 16, "album", "raw", ".cue", "case"),
		Entry("case mismatch original track", 24, "track1", "raw", ".cue", "case"),
		Entry("symlinked sheet with unrelated broken link", 16, "album", "raw", ".cue", "symlink"))

	It("downloads a converted APE CUE track with a FLAC filename and content type", func() {
		DeferCleanup(configtest.SetupConfig())
		conf.Server.EnableDownloads = true
		conf.Server.CacheFolder = conf.NewDir(GinkgoT().TempDir())
		conf.Server.TranscodingCacheSize = "10MB"
		name, err := filepath.Abs("tests/fixtures/cue-ape/stereo-16.ape")
		Expect(err).NotTo(HaveOccurred())
		repo := &tests.MockMediaFileRepo{}
		repo.SetData(model.MediaFiles{{ID: "track1", Path: name, Suffix: "ape", Title: "First", CueTrack: 1,
			CueEndSample: 92 * 588, SampleRate: 44100, Channels: 2, BitDepth: new(16), Duration: 92.0 / 75}})
		ds := &tests.MockDataStore{MockedMediaFile: repo, MockedTranscoding: cueDownloadTranscodingRepo{}}
		ff := ffmpeg.New()
		cache := stream.NewTranscodingCache()
		Eventually(func() bool { return cache.Available(GinkgoT().Context()) }, 10*time.Second).Should(BeTrue())
		router := &Router{ds: ds, streamer: stream.NewMediaStreamer(ds, ff, cache), transcodeDecision: stream.NewTranscodeDecider(ds, ff)}
		w := httptest.NewRecorder()
		_, err = router.Download(w, newGetRequest("id=track1&format=flac"))
		Expect(err).NotTo(HaveOccurred())
		Expect(w.Body.String()).To(HavePrefix("fLaC"))
		Expect(w.Header().Get("Content-Type")).To(Equal(mime.TypeByExtension(".flac")))
		_, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
		Expect(err).NotTo(HaveOccurred())
		Expect(params["filename"]).To(Equal("First.flac"))
	})

	It("keeps mixed and multi-image albums as archives", func() {
		Expect(singleCUESource(nil)).To(BeNil())
		Expect(singleCUESource(model.MediaFiles{{Path: "one.flac", CueTrack: 1}, {Path: "two.flac", CueTrack: 2}})).To(BeNil())
		Expect(singleCUESource(model.MediaFiles{{Path: "one.flac", CueTrack: 1}, {Path: "one.flac"}})).To(BeNil())
	})
})
