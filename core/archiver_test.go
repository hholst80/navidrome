package core_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing/iotest"

	"github.com/Masterminds/squirrel"
	"github.com/navidrome/navidrome/core"
	"github.com/navidrome/navidrome/core/stream"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/persistence"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
)

var _ = Describe("Archiver", func() {
	var (
		arch core.Archiver
		ms   *mockMediaStreamer
		ds   *mockDataStore
		sh   *mockShare
	)

	BeforeEach(func() {
		ms = &mockMediaStreamer{}
		sh = &mockShare{}
		ds = &mockDataStore{}
		arch = core.NewArchiver(ms, ds, sh)
	})

	Context("ZipAlbum", func() {
		It("zips an album correctly", func() {
			mfs := model.MediaFiles{
				{Path: "test_data/01 - track1.mp3", Suffix: "mp3", AlbumID: "1", Album: "Album/Promo", DiscNumber: 1},
				{Path: "test_data/02 - track2.mp3", Suffix: "mp3", AlbumID: "1", Album: "Album/Promo", DiscNumber: 1},
			}

			mfRepo := &mockMediaFileRepository{}
			mfRepo.On("GetAll", []model.QueryOptions{{
				Filters: squirrel.Eq{"album_id": "1", "missing": false},
				Sort:    "album",
			}}).Return(mfs, nil)

			ds.On("MediaFile", mock.Anything).Return(mfRepo)
			ms.On("NewStream", mock.Anything, mock.Anything, stream.Request{Format: "mp3", BitRate: 128}).Return(io.NopCloser(strings.NewReader("test")), nil).Times(3)

			out := new(bytes.Buffer)
			err := arch.ZipAlbum(context.Background(), "1", "mp3", 128, out)
			Expect(err).To(BeNil())

			zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
			Expect(err).To(BeNil())

			Expect(len(zr.File)).To(Equal(2))
			Expect(zr.File[0].Name).To(Equal("Album_Promo/01 - track1.mp3"))
			Expect(zr.File[1].Name).To(Equal("Album_Promo/02 - track2.mp3"))
		})
	})

	Context("ZipArtist", func() {
		It("zips an artist's albums correctly", func() {
			mfs := model.MediaFiles{
				{Path: "test_data/01 - track1.mp3", Suffix: "mp3", AlbumArtistID: "1", AlbumID: "1", Album: "Album 1", DiscNumber: 1},
				{Path: "test_data/02 - track2.mp3", Suffix: "mp3", AlbumArtistID: "1", AlbumID: "1", Album: "Album 1", DiscNumber: 1},
			}

			mfRepo := &mockMediaFileRepository{}
			mfRepo.On("GetAll", []model.QueryOptions{{
				Filters: squirrel.And{
					persistence.ParticipantIDFilter("media_file", "1", model.RoleAlbumArtist),
					squirrel.Eq{"missing": false},
				},
				Sort: "album",
			}}).Return(mfs, nil)

			ds.On("MediaFile", mock.Anything).Return(mfRepo)
			ms.On("NewStream", mock.Anything, mock.Anything, stream.Request{Format: "mp3", BitRate: 128}).Return(io.NopCloser(strings.NewReader("test")), nil).Times(2)

			out := new(bytes.Buffer)
			err := arch.ZipArtist(context.Background(), "1", "mp3", 128, out)
			Expect(err).To(BeNil())

			zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
			Expect(err).To(BeNil())

			Expect(len(zr.File)).To(Equal(2))
			Expect(zr.File[0].Name).To(Equal("Album 1/01 - track1.mp3"))
			Expect(zr.File[1].Name).To(Equal("Album 1/02 - track2.mp3"))
		})
	})

	DescribeTable("returns track failures without continuing the archive",
		func(kind string, readFailure bool) {
			tracks := model.MediaFiles{
				{ID: "1", Path: "album.flac", CueTrack: 1, Title: "First", Suffix: "flac", Album: "Album", AlbumID: "1"},
				{ID: "2", Path: "album.flac", CueTrack: 2, Title: "Second", Suffix: "flac", Album: "Album", AlbumID: "1"},
			}
			failure := errors.New("track extraction failed")
			ms.On("NewStream", mock.Anything, &tracks[0], mock.Anything).
				Return(io.NopCloser(strings.NewReader("first track")), nil).Once()
			if readFailure {
				ms.On("NewStream", mock.Anything, &tracks[1], mock.Anything).
					Return(io.NopCloser(iotest.ErrReader(failure)), nil).Once()
			} else {
				ms.On("NewStream", mock.Anything, &tracks[1], mock.Anything).Return(nil, failure).Once()
			}
			out := new(bytes.Buffer)
			var err error
			switch kind {
			case "album":
				repo := &mockMediaFileRepository{}
				repo.On("GetAll", mock.Anything).Return(tracks, nil)
				ds.On("MediaFile", mock.Anything).Return(repo)
				err = arch.ZipAlbum(context.Background(), "1", "flac", 0, out)
			case "share":
				err = arch.ZipShare(context.Background(), &model.Share{ID: "1", Downloadable: true, Format: "raw", Tracks: tracks}, out)
			case "playlist":
				repo := &mockPlaylistRepository{}
				repo.On("GetWithTracks", "1", true, false).Return(&model.Playlist{ID: "1", Name: "Playlist",
					Tracks: []model.PlaylistTrack{{MediaFile: tracks[0]}, {MediaFile: tracks[1]}}}, nil)
				ds.On("Playlist", mock.Anything).Return(repo)
				err = arch.ZipPlaylist(context.Background(), "1", "raw", 0, out)
			}
			Expect(err).To(MatchError(failure))
			Expect(out.Len()).To(BeZero())
			ms.AssertNumberOfCalls(GinkgoT(), "NewStream", 2)
		},
		Entry("album stream creation", "album", false),
		Entry("album stream reading", "album", true),
		Entry("share stream creation", "share", false),
		Entry("share stream reading", "share", true),
		Entry("playlist stream creation", "playlist", false),
		Entry("playlist stream reading", "playlist", true),
	)

	DescribeTable("copies each original CUE source once despite a historical ordinary row", func(format string, ordinaryFirst bool) {
		source := filepath.Join(GinkgoT().TempDir(), "album.flac")
		original := []byte("unchanged album source")
		Expect(os.WriteFile(source, original, 0600)).To(Succeed())
		ordinary := model.MediaFile{ID: "old", Path: source, Suffix: "flac", Album: "Album", AlbumID: "1", Missing: true}
		tracks := model.MediaFiles{{ID: "cue1", Path: source, Suffix: "flac", Album: "Album", AlbumID: "1", CueTrack: 1}, {ID: "cue2", Path: source, Suffix: "flac", Album: "Album", AlbumID: "1", CueTrack: 2}}
		if ordinaryFirst {
			tracks = append(model.MediaFiles{ordinary}, tracks...)
		} else {
			tracks = append(tracks, ordinary)
		}
		repo := &mockMediaFileRepository{}
		repo.On("GetAll", mock.Anything).Return(tracks, nil)
		ds.On("MediaFile", mock.Anything).Return(repo)
		var out bytes.Buffer
		Expect(arch.ZipAlbum(context.Background(), "1", format, 0, &out)).To(Succeed())
		zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
		Expect(err).NotTo(HaveOccurred())
		Expect(zr.File).To(HaveLen(1))
		r, err := zr.File[0].Open()
		Expect(err).NotTo(HaveOccurred())
		data, err := io.ReadAll(r)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Close()).To(Succeed())
		Expect(data).To(Equal(original))
		ms.AssertNumberOfCalls(GinkgoT(), "NewStream", 0)
	}, Entry("raw ordinary first", "raw", true), Entry("raw ordinary last", "raw", false), Entry("default ordinary first", "", true), Entry("default ordinary last", "", false))

	It("disambiguates colliding names from separate CUE images and existing suffixed names", func() {
		tracks := model.MediaFiles{
			{ID: "1", Path: "one.flac", CueTrack: 1, Title: "Same/Title", Suffix: "flac", Album: "Album", AlbumID: "1"},
			{ID: "2", Path: "two.flac", CueTrack: 1, Title: "Same_Title", Suffix: "flac", Album: "Album", AlbumID: "1"},
			{ID: "3", Path: "three.flac", CueTrack: 1, Title: "Same_Title (2)", Suffix: "flac", Album: "Album", AlbumID: "1"},
			{ID: "4", Path: "four.flac", CueTrack: 1, Title: "same_title", Suffix: "flac", Album: "Album", AlbumID: "1"},
		}
		repo := &mockMediaFileRepository{}
		repo.On("GetAll", mock.Anything).Return(tracks, nil)
		ds.On("MediaFile", mock.Anything).Return(repo)
		for i := range tracks {
			track := tracks[i]
			ms.On("NewStream", mock.Anything, &track, mock.Anything).
				Return(io.NopCloser(strings.NewReader(track.ID)), nil).Once()
		}
		out := new(bytes.Buffer)
		Expect(arch.ZipAlbum(context.Background(), "1", "flac", 0, out)).To(Succeed())
		zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
		Expect(err).NotTo(HaveOccurred())
		Expect(zr.File).To(HaveLen(4))
		expected := []string{"01 - Same_Title.flac", "01 - Same_Title (2).flac", "01 - Same_Title (2) (2).flac", "01 - same_title (3).flac"}
		for i, entry := range zr.File {
			Expect(entry.Name).To(Equal("Album/" + expected[i]))
			r, err := entry.Open()
			Expect(err).NotTo(HaveOccurred())
			data, err := io.ReadAll(r)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Close()).To(Succeed())
			Expect(string(data)).To(Equal(tracks[i].ID))
		}
	})

	Context("when the transcode limiter rejects a file", func() {
		It("aborts the archive instead of continuing with empty entries", func() {
			mfs := model.MediaFiles{
				{Path: "test_data/01 - track1.mp3", Suffix: "mp3", AlbumID: "1", Album: "Album", DiscNumber: 1},
				{Path: "test_data/02 - track2.mp3", Suffix: "mp3", AlbumID: "1", Album: "Album", DiscNumber: 1},
			}

			mfRepo := &mockMediaFileRepository{}
			mfRepo.On("GetAll", []model.QueryOptions{{
				Filters: squirrel.Eq{"album_id": "1", "missing": false},
				Sort:    "album",
			}}).Return(mfs, nil)
			ds.On("MediaFile", mock.Anything).Return(mfRepo)

			ms.On("NewStream", mock.Anything, mock.Anything, stream.Request{Format: "mp3", BitRate: 128}).
				Return(nil, stream.ErrTooManyTranscodes).Once()

			out := new(bytes.Buffer)
			err := arch.ZipAlbum(context.Background(), "1", "mp3", 128, out)
			Expect(err).To(MatchError(stream.ErrTooManyTranscodes))
			Expect(out.Len()).To(BeZero())
			// NewStream should only have been called once: the loop must bail
			// out on the rejection instead of trying every remaining track.
			ms.AssertNumberOfCalls(GinkgoT(), "NewStream", 1)
		})
	})

	Context("ZipShare", func() {
		It("zips a share correctly", func() {
			mfs := model.MediaFiles{
				{ID: "1", Path: "test_data/01 - track1.mp3", Suffix: "mp3", Artist: "Artist 1", Title: "track1"},
				{ID: "2", Path: "test_data/02 - track2.mp3", Suffix: "mp3", Artist: "Artist 2", Title: "track2"},
			}

			share := &model.Share{
				ID:           "1",
				Downloadable: true,
				Format:       "mp3",
				MaxBitRate:   128,
				Tracks:       mfs,
			}

			ms.On("NewStream", mock.Anything, mock.Anything, stream.Request{Format: "mp3", BitRate: 128}).Return(io.NopCloser(strings.NewReader("test")), nil).Times(2)

			out := new(bytes.Buffer)
			err := arch.ZipShare(context.Background(), share, out)
			Expect(err).To(BeNil())

			// Share.Load records a visit; re-loading here would double-count
			// every download.
			sh.AssertNotCalled(GinkgoT(), "Load", mock.Anything, mock.Anything)

			zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
			Expect(err).To(BeNil())

			Expect(len(zr.File)).To(Equal(2))
			Expect(zr.File[0].Name).To(Equal("01 - Artist 1 - track1.mp3"))
			Expect(zr.File[1].Name).To(Equal("02 - Artist 2 - track2.mp3"))

		})
	})

	Context("ZipPlaylist", func() {
		It("zips a playlist correctly", func() {
			tracks := []model.PlaylistTrack{
				{MediaFile: model.MediaFile{Path: "test_data/01 - track1.mp3", Suffix: "mp3", AlbumID: "1", Album: "Album 1", DiscNumber: 1, Artist: "AC/DC", Title: "track1"}},
				{MediaFile: model.MediaFile{Path: "test_data/02 - track2.mp3", Suffix: "mp3", AlbumID: "1", Album: "Album 1", DiscNumber: 1, Artist: "Artist 2", Title: "track2"}},
			}

			pls := &model.Playlist{
				ID:     "1",
				Name:   "Test Playlist",
				Tracks: tracks,
			}

			plRepo := &mockPlaylistRepository{}
			plRepo.On("GetWithTracks", "1", true, false).Return(pls, nil)
			ds.On("Playlist", mock.Anything).Return(plRepo)
			ms.On("NewStream", mock.Anything, mock.Anything, stream.Request{Format: "mp3", BitRate: 128}).Return(io.NopCloser(strings.NewReader("test")), nil).Times(2)

			out := new(bytes.Buffer)
			err := arch.ZipPlaylist(context.Background(), "1", "mp3", 128, out)
			Expect(err).To(BeNil())

			zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
			Expect(err).To(BeNil())

			Expect(len(zr.File)).To(Equal(3))
			Expect(zr.File[0].Name).To(Equal("01 - AC_DC - track1.mp3"))
			Expect(zr.File[1].Name).To(Equal("02 - Artist 2 - track2.mp3"))
			Expect(zr.File[2].Name).To(Equal("Test Playlist.m3u"))

			// Verify M3U content
			m3uFile, err := zr.File[2].Open()
			Expect(err).To(BeNil())
			defer m3uFile.Close()

			m3uContent, err := io.ReadAll(m3uFile)
			Expect(err).To(BeNil())

			expectedM3U := "#EXTM3U\n#PLAYLIST:Test Playlist\n#EXTINF:0,AC/DC - track1\n01 - AC_DC - track1.mp3\n#EXTINF:0,Artist 2 - track2\n02 - Artist 2 - track2.mp3\n"
			Expect(string(m3uContent)).To(Equal(expectedM3U))
		})
	})
})

type mockDataStore struct {
	mock.Mock
	model.DataStore
}

func (m *mockDataStore) MediaFile(ctx context.Context) model.MediaFileRepository {
	args := m.Called(ctx)
	return args.Get(0).(model.MediaFileRepository)
}

func (m *mockDataStore) Playlist(ctx context.Context) model.PlaylistRepository {
	args := m.Called(ctx)
	return args.Get(0).(model.PlaylistRepository)
}

func (m *mockDataStore) Library(context.Context) model.LibraryRepository {
	return &mockLibraryRepository{}
}

type mockLibraryRepository struct {
	mock.Mock
	model.LibraryRepository
}

func (m *mockLibraryRepository) GetPath(id int) (string, error) {
	return "/music", nil
}

type mockMediaFileRepository struct {
	mock.Mock
	model.MediaFileRepository
}

func (m *mockMediaFileRepository) GetAll(options ...model.QueryOptions) (model.MediaFiles, error) {
	args := m.Called(options)
	return args.Get(0).(model.MediaFiles), args.Error(1)
}

type mockPlaylistRepository struct {
	mock.Mock
	model.PlaylistRepository
}

func (m *mockPlaylistRepository) GetWithTracks(id string, refreshSmartPlaylists, includeMissing bool) (*model.Playlist, error) {
	args := m.Called(id, refreshSmartPlaylists, includeMissing)
	return args.Get(0).(*model.Playlist), args.Error(1)
}

type mockMediaStreamer struct {
	mock.Mock
	stream.MediaStreamer
}

func (m *mockMediaStreamer) NewStream(ctx context.Context, mf *model.MediaFile, req stream.Request) (*stream.Stream, error) {
	args := m.Called(ctx, mf, req)
	if args.Error(1) != nil {
		return nil, args.Error(1)
	}
	return &stream.Stream{ReadCloser: args.Get(0).(io.ReadCloser)}, nil
}

type mockShare struct {
	mock.Mock
	core.Share
}

func (m *mockShare) Load(ctx context.Context, id string) (*model.Share, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(*model.Share), args.Error(1)
}
