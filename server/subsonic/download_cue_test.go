package subsonic

import (
	"context"
	"github.com/Masterminds/squirrel"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
