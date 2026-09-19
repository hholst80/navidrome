package gotaglib

import (
	"fmt"
	"os"
	"strings"

	"github.com/navidrome/navidrome/model/metadata"
	"github.com/navidrome/navidrome/model/metadata/cue"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("APE CUE metadata", func() {
	DescribeTable("extracts APEv2 CUESHEET tags and source precision", func(bits, rate int) {
		name := fmt.Sprintf("tests/fixtures/cue-ape/stereo-%d.ape", bits)
		e := extractor{fs: os.DirFS(".")}
		info, err := e.extractMetadata(name)
		Expect(err).NotTo(HaveOccurred())
		stat, err := os.Stat(name)
		Expect(err).NotTo(HaveOccurred())
		info.FileInfo = testFileInfo{stat}
		md := metadata.New(name, *info)
		text, err := os.ReadFile(strings.TrimSuffix(name, ".ape") + ".cue")
		Expect(err).NotTo(HaveOccurred())
		Expect(md.CUEText()).To(Equal(string(text)))
		sheet, err := cue.ReadCue(strings.NewReader(md.CUEText()))
		Expect(err).NotTo(HaveOccurred())
		tracks, err := md.CUETracks(sheet, 1, "folder")
		Expect(err).NotTo(HaveOccurred())
		Expect(tracks).To(HaveLen(2))
		Expect(tracks[0].Title).To(Equal("First"))
		Expect(tracks[1].Title).To(Equal("Second"))
		Expect(tracks[0].AlbumArtist).To(Equal("Synthetic Artist"))
		Expect(tracks[0].Album).To(Equal("Synthetic Album"))
		Expect(tracks[0].CueStartSample).To(BeZero())
		Expect(tracks[0].CueEndSample).To(Equal(int64(92 * (rate / 75))))
		Expect(tracks[1].CueStartSample).To(Equal(tracks[0].CueEndSample))
		Expect(tracks[1].CueEndSample).To(BeZero())
		for _, track := range tracks {
			Expect(track.Path).To(Equal(name))
			Expect(track.Suffix).To(Equal("ape"))
			Expect(track.SampleRate).To(Equal(rate))
			Expect(track.Channels).To(Equal(2))
			Expect(track.BitDepth).To(Equal(&bits))
		}
	}, Entry("16-bit 44.1 kHz", 16, 44100), Entry("24-bit 96 kHz", 24, 96000))
})
