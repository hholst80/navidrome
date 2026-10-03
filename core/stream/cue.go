package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/navidrome/navidrome/core/ffmpeg"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/utils/pcmwave"
)

func (ms *mediaStreamer) newCUEStream(ctx context.Context, mf *model.MediaFile, req Request) (*Stream, error) {
	var source *pcmwave.Source
	var input io.ReaderAt
	var closer io.Closer
	switch strings.ToLower(mf.Suffix) {
	case "wav":
		f, err := os.Open(mf.AbsolutePath())
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		source, err = pcmwave.Parse(f, info.Size())
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		input, closer = f, f
	case "flac", "ape":
		var err error
		source, err = ffmpeg.ProbePCM(ctx, mf.AbsolutePath())
		if err != nil {
			return nil, err
		}
		// Reserve one request slot before HTTP headers are sent, so a busy
		// decoder can produce a normal rate-limit response. A seek restarts
		// the decoder within the same slot.
		release, err := ms.limiter.Acquire(ctx, limiterKey(ctx))
		if err != nil {
			return nil, err
		}
		acquire := func(context.Context) (func(), error) { return func() {}, nil }
		decoder := ffmpeg.NewPCMReader(ctx, mf.AbsolutePath(), source, mf.CueEndSample, acquire)
		input, closer = decoder, &cueSourceCloser{Closer: decoder, release: release}
	default:
		return nil, fmt.Errorf("CUE sources must be PCM WAV, FLAC or APE")
	}
	keep := false
	defer func() {
		if !keep {
			_ = closer.Close()
		}
	}()
	if source.Rate != mf.SampleRate || req.Offset < 0 || mf.CueStartSample < 0 ||
		int64(req.Offset) > (math.MaxInt64-mf.CueStartSample)/int64(source.Rate) {
		return nil, fmt.Errorf("invalid CUE seek or changed WAV sample rate")
	}
	start := mf.CueStartSample + int64(req.Offset)*int64(source.Rate)
	reader, err := source.Segment(input, start, mf.CueEndSample, [][2]string{
		{"INAM", mf.Title}, {"IART", mf.Artist}, {"IPRD", mf.Album},
		{"IPRT", strconv.Itoa(mf.TrackNumber)}, {"ICRD", mf.Date},
	})
	if err != nil {
		return nil, err
	}
	r := &cueWAVReader{SectionReader: reader, source: closer}
	format := req.Format
	if !cueNeedsConversion(req, source) {
		keep = true
		return &Stream{ctx: ctx, mf: mf, format: "wav", ReadCloser: r, Seeker: r}, nil
	}
	// Explicit conversion consumes the same virtual WAV as direct play. It uses
	// a pipe, never the disk cache or a temporary audio file.
	command := LookupTranscodeCommand(ctx, ms.ds, format)
	if command == "" && format != "wav" {
		return nil, fmt.Errorf("unsupported CUE output format: %s", format)
	}
	release := func() {}
	if strings.EqualFold(mf.Suffix, "wav") {
		release, err = ms.limiter.Acquire(ctx, limiterKey(ctx))
		if err != nil {
			return nil, err
		}
	}
	transcodeCtx, cancel := context.WithCancel(ctx)
	if req.BitDepth == 0 && (format == "wav" || format == "flac") {
		req.BitDepth = source.Bits
	}
	end := mf.CueEndSample
	if end == 0 {
		end = source.Samples()
	}
	out, err := ms.transcoder.Transcode(transcodeCtx, ffmpeg.TranscodeOptions{
		Input: r, Command: command, FilePath: mf.AbsolutePath(), Format: format,
		BitRate: req.BitRate, SampleRate: req.SampleRate, Channels: req.Channels, BitDepth: req.BitDepth,
		Duration: float32(float64(end-start) / float64(source.Rate)),
	})
	if err != nil {
		cancel()
		release()
		return nil, err
	}
	keep = true
	return &Stream{ctx: ctx, mf: mf, format: format, bitRate: req.BitRate,
		ReadCloser: &cueTranscodeReader{ReadCloser: out, input: r, cancel: cancel, release: release}}, nil
}

type cueWAVReader struct {
	*io.SectionReader
	source io.Closer
}

func (r *cueWAVReader) Close() error { return r.source.Close() }

type cueTranscodeReader struct {
	io.ReadCloser
	input   io.Closer
	cancel  context.CancelFunc
	release func()
}

func (r *cueTranscodeReader) Close() error {
	r.cancel()
	err := errors.Join(r.ReadCloser.Close(), r.input.Close())
	r.release()
	return err
}

func cueNeedsConversion(req Request, source *pcmwave.Source) bool {
	if req.Format == "" || req.Format == "raw" {
		return false
	}
	return req.Format != "wav" || (req.SampleRate != 0 && req.SampleRate != source.Rate) ||
		(req.Channels != 0 && req.Channels != source.Channels) || (req.BitDepth != 0 && req.BitDepth != source.Bits)
}

// OutputFormat describes the bytes returned for a virtual track, independently
// of the physical album image's container.
func OutputFormat(mf *model.MediaFile, requested string) string {
	if requested != "" && requested != "raw" {
		return requested
	}
	if mf.CueTrack > 0 {
		return "wav"
	}
	return mf.Suffix
}

type cueSourceCloser struct {
	io.Closer
	release func()
}

func (c *cueSourceCloser) Close() error { err := c.Closer.Close(); c.release(); return err }
