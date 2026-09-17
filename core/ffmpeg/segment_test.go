package ffmpeg

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CUE WAV extraction", func() {
	It("extracts a standalone WAV without a configured command", func() {
		binary, err := ffmpegCmd()
		Expect(err).NotTo(HaveOccurred())
		source := filepath.Join(GinkgoT().TempDir(), "source.wav")
		cmd := exec.CommandContext(context.Background(), binary, "-v", "error", "-f", "lavfi", "-i",
			"sine=frequency=440:sample_rate=44100:duration=2", "-c:a", "pcm_s16le", source)
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		opts := TranscodeOptions{FilePath: source, Format: "wav", BitDepth: 16,
			Segment: &AudioSegment{StartSample: 44100, EndSample: 88200, SourceRate: 44100}}
		result, err := New().Transcode(context.Background(), opts)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(result.Close)
		data, err := io.ReadAll(result)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data[:4])).To(Equal("RIFF"))
		Expect(string(data[8:12])).To(Equal("WAVE"))
		// One second of mono 16-bit PCM plus the WAV headers.
		Expect(len(data)).To(BeNumerically(">=", 88200))
		Expect(len(data)).To(BeNumerically("<", 89200))
		opts.Command = "custom encoder"
		_, err = segmentArgs(opts)
		Expect(err).To(MatchError("custom transcoding commands are unsupported for CUE tracks"))
	})
})
