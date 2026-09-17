package persistence

import (
	"context"
	"io/fs"
	"strings"
	"testing/fstest"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/metadata"
	"github.com/navidrome/navidrome/model/metadata/cue"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type cueFileInfo struct{ fs.FileInfo }

func (f cueFileInfo) BirthTime() time.Time { return f.ModTime() }

var _ = Describe("CUE track persistence", func() {
	It("keeps tracks sharing a source separate and preserves their IDs on rescan", func() {
		repo := NewMediaFileRepository(context.Background(), GetDBXBuilder())
		const source = "cue-regression/album.flac"
		DeferCleanup(func() {
			_, err := GetDBXBuilder().NewQuery("DELETE FROM media_file WHERE path = {:path}").
				Bind(map[string]interface{}{"path": source}).Execute()
			Expect(err).NotTo(HaveOccurred())
		})
		files := fstest.MapFS{source: &fstest.MapFile{Data: []byte("audio"), ModTime: time.Now()}}
		info, err := fs.Stat(files, source)
		Expect(err).NotTo(HaveOccurred())
		md := metadata.New(source, metadata.Info{
			FileInfo:        cueFileInfo{info},
			AudioProperties: metadata.AudioProperties{SampleRate: 44100, Duration: 2 * time.Minute},
		})
		sheet, err := cue.ReadCue(strings.NewReader(`FILE "album.flac" WAVE
  TRACK 01 AUDIO
    TITLE "First"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Second"
    INDEX 01 01:00:00
`))
		Expect(err).NotTo(HaveOccurred())
		ordinary := md.ToMediaFile(1, "")
		Expect(repo.Put(&ordinary)).To(Succeed())
		ids := []string{ordinary.ID}
		for scan := range 2 {
			tracks, err := md.CUETracks(sheet, 1, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(tracks).To(HaveLen(2))
			for i := range tracks {
				Expect(tracks[i].ID).To(BeEmpty())
				Expect(repo.Put(&tracks[i])).To(Succeed())
				if scan == 0 {
					Expect(ids).NotTo(ContainElement(tracks[i].ID))
					ids = append(ids, tracks[i].ID)
				} else {
					Expect(tracks[i].ID).To(Equal(ids[i+1]))
				}
				stored, err := repo.Get(tracks[i].ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(stored.Title).To(Equal(tracks[i].Title))
				Expect(stored.CueTrack).To(Equal(i + 1))
				Expect(stored.CueStartSample).To(Equal(tracks[i].CueStartSample))
				Expect(stored.CueEndSample).To(Equal(tracks[i].CueEndSample))
			}
			sheet.File[0].Tracks[0].Title = "Updated first"
		}
		stored, err := repo.GetAll(model.QueryOptions{Filters: squirrel.Eq{"media_file.path": source}})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(3))
	})
})
