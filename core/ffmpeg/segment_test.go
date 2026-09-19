package ffmpeg

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
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

var _ = Describe("APE CUE sample preservation", func() {
	DescribeTable("preserves source samples across boundaries and seeks", func(bits, rate int, checksum string) {
		binary, err := ffmpegCmd()
		Expect(err).NotTo(HaveOccurred())
		ctx := GinkgoT().Context()
		source := fmt.Sprintf("tests/fixtures/cue-ape/stereo-%d.ape", bits)
		decode := func(name string) []byte {
			pcm, err := exec.CommandContext(ctx, binary, "-v", "error", "-i", name,
				"-f", fmt.Sprintf("s%dle", bits), "-").Output()
			Expect(err).NotTo(HaveOccurred())
			return pcm
		}
		original := decode(source)
		Expect(fmt.Sprintf("%x", sha256.Sum256(original))).To(Equal(checksum))
		frameSize := 2 * (bits / 8)
		Expect(original).To(HaveLen(3 * rate * frameSize))
		boundary := int64(92 * (rate / 75))
		for _, format := range []string{"flac", "wav"} {
			extract := func(start, end int64, offset int) []byte {
				stream, err := New().Transcode(ctx, TranscodeOptions{FilePath: source, Format: format,
					Command: defaultCommands[format], BitDepth: bits, SampleRate: rate, Channels: 2,
					Offset: offset, Segment: &AudioSegment{StartSample: start, EndSample: end, SourceRate: rate,
						Tags: map[string]string{"title": "Extracted track"}}})
				Expect(err).NotTo(HaveOccurred())
				data, err := io.ReadAll(stream)
				Expect(err).NotTo(HaveOccurred())
				Expect(stream.Close()).To(Succeed())
				name := filepath.Join(GinkgoT().TempDir(), "track."+format)
				Expect(os.WriteFile(name, data, 0600)).To(Succeed())
				tags, err := exec.CommandContext(ctx, binary, "-v", "error", "-i", name, "-f", "ffmetadata", "-").Output()
				Expect(err).NotTo(HaveOccurred())
				Expect(string(tags)).To(ContainSubstring("title=Extracted track"))
				Expect(string(tags)).NotTo(ContainSubstring("CUESHEET"))
				return decode(name)
			}
			first, last := extract(0, boundary, 0), extract(boundary, 0, 0)
			Expect(first).To(Equal(original[:int(boundary)*frameSize]))
			Expect(append(first, last...)).To(Equal(original))
			Expect(extract(boundary, 0, 1)).To(Equal(original[(int(boundary)+rate)*frameSize:]))
		}
	}, Entry("16-bit stereo 44.1 kHz", 16, 44100, "23d363f881949499e25568fda5fd02d54c73a42b7cddb4ec45eaae874b166398"),
		Entry("24-bit stereo 96 kHz", 24, 96000, "16dd91941379324cfcf105f751d27d6e1a52f4038d1586c0348a052c69ca5235"))
})

var _ = Describe("CUE sample preservation", func() {
	DescribeTable("preserves 24-bit stereo samples across boundaries and seeks", func(rate int, format string) {
		binary, err := ffmpegCmd()
		Expect(err).NotTo(HaveOccurred())
		ctx := GinkgoT().Context()
		dir := GinkgoT().TempDir()
		source := filepath.Join(dir, "source.wav")
		output, err := exec.CommandContext(ctx, binary, "-v", "error", "-f", "lavfi", "-i",
			fmt.Sprintf("aevalsrc=0.3*sin(2*PI*997*t)|0.2*sin(2*PI*1231*t):s=%d:d=3", rate),
			"-c:a", "pcm_s24le", source).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(output))
		decode := func(name string) []byte {
			pcm, err := exec.CommandContext(ctx, binary, "-v", "error", "-i", name, "-f", "s24le", "-c:a", "pcm_s24le", "-").Output()
			Expect(err).NotTo(HaveOccurred())
			return pcm
		}
		original := decode(source)
		Expect(original).To(HaveLen(3 * rate * 2 * 3))
		boundary := int64(rate + 17*(rate/75))
		extract := func(start, end int64, offset int) []byte {
			stream, err := New().Transcode(ctx, TranscodeOptions{FilePath: source, Format: format, Command: defaultCommands[format], BitDepth: 24,
				Offset: offset, Segment: &AudioSegment{StartSample: start, EndSample: end, SourceRate: rate}})
			Expect(err).NotTo(HaveOccurred())
			data, err := io.ReadAll(stream)
			Expect(err).NotTo(HaveOccurred())
			Expect(stream.Close()).To(Succeed())
			name := filepath.Join(dir, fmt.Sprintf("%d-%d.%s", start, offset, format))
			Expect(os.WriteFile(name, data, 0600)).To(Succeed())
			return decode(name)
		}
		first := extract(0, boundary, 0)
		last := extract(boundary, 0, 0)
		Expect(first).To(HaveLen(int(boundary) * 6))
		Expect(append(first, last...)).To(Equal(original))
		Expect(extract(boundary, 0, 1)).To(Equal(original[(boundary+int64(rate))*6:]))
	}, Entry("44.1 kHz FLAC", 44100, "flac"), Entry("48 kHz WAV", 48000, "wav"), Entry("96 kHz FLAC", 96000, "flac"))
})
