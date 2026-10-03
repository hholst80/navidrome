package subsonic

import (
	"context"
	"github.com/Masterminds/squirrel"
	"io"
	"mime"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/conf/configtest"
	"github.com/navidrome/navidrome/core"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/tests"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type cueDownloadArchiver struct {
	core.Archiver
	format string
}

func (a *cueDownloadArchiver) ZipAlbum(_ context.Context, _ string, format string, _ int, w io.Writer) error {
	a.format = format
	_, err := io.WriteString(w, "converted album archive")
	return err
}

var _ = Describe("CUE original downloads", func() {
	DescribeTable("distinguishes original files from converted tracks", func(id, format string, original bool) {
		DeferCleanup(configtest.SetupConfig())
		conf.Server.EnableDownloads = true
		conf.Server.AutoTranscodeDownload = false
		name := filepath.Join(GinkgoT().TempDir(), "original album.flac")
		source := []byte("fLaC unchanged complete album image")
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
			Expect(params["filename"]).To(Equal("original album.flac"))
			Expect(archiver.format).To(BeEmpty())
			if id == "album" {
				Expect(repo.Options.Filters).To(Equal(squirrel.Eq{"album_id": "album", "missing": false}))
			}
		} else {
			Expect(archiver.format).To(Equal("flac"))
			Expect(w.Header().Get("Content-Type")).To(Equal("application/zip"))
		}
	}, Entry("original album", "album", "raw", true), Entry("default album", "album", "", true), Entry("original track", "track1", "raw", true), Entry("converted FLAC album", "album", "flac", false))

	It("keeps mixed and multi-image albums as archives", func() {
		Expect(singleCUESource(nil)).To(BeNil())
		Expect(singleCUESource(model.MediaFiles{{Path: "one.flac", CueTrack: 1}, {Path: "two.flac", CueTrack: 2}})).To(BeNil())
		Expect(singleCUESource(model.MediaFiles{{Path: "one.flac", CueTrack: 1}, {Path: "one.flac"}})).To(BeNil())
	})
})
