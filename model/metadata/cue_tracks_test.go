package metadata_test

import (
	"os"
	"strings"
	"time"

	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/metadata"
	"github.com/navidrome/navidrome/model/metadata/cue"
	"github.com/navidrome/navidrome/tests"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CUE disc metadata", func() {
	DescribeTable("maps sheet disc metadata while retaining source fallbacks",
		func(rem, sourceDisc string, expectedDisc int, expectedTotal string) {
			_, filePath, _ := tests.TempFile(GinkgoT(), "cue", ".flac")
			info, err := os.Stat(filePath)
			Expect(err).NotTo(HaveOccurred())
			md := metadata.New("album.flac", metadata.Info{
				FileInfo:        testFileInfo{info},
				Tags:            model.RawTags{"discnumber": {sourceDisc}},
				AudioProperties: metadata.AudioProperties{Duration: time.Minute, SampleRate: 44100},
			})
			sheet, err := cue.ReadCue(strings.NewReader("FILE \"album.flac\" WAVE\n" + rem +
				"  TRACK 01 AUDIO\n    INDEX 01 00:00:00\n"))
			Expect(err).NotTo(HaveOccurred())
			tracks, err := md.CUETracks(sheet, 1, "folder")
			Expect(err).NotTo(HaveOccurred())
			Expect(tracks).To(HaveLen(1))
			Expect(tracks[0].DiscNumber).To(Equal(expectedDisc))
			if expectedTotal != "" {
				Expect(tracks[0].Tags[model.TagTotalDiscs]).To(Equal([]string{expectedTotal}))
			}
		},
		Entry("sheet only", "REM DISCNUMBER 2\nREM TOTALDISCS 3\n", "", 2, "3"),
		Entry("sheet overrides source", "REM DISCNUMBER 2\nREM TOTALDISCS 3\n", "1/2", 2, "3"),
		Entry("source fallback", "", "2/3", 2, ""),
		Entry("disc only retains source total", "REM DISCNUMBER 2\n", "1/3", 2, "3"),
		Entry("total only retains source disc", "REM TOTALDISCS 4\n", "2", 2, "4"),
	)
})
