package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
)

// AudioSegment describes a range of decoded source samples. EndSample is
// exclusive; zero means EOF. SourceRate is independent of the output rate.
type AudioSegment struct {
	StartSample int64
	EndSample   int64
	SourceRate  int
	Tags        map[string]string
}

// transcodeSegment uses the same codec and constraint builder as ordinary
// streams, but trims decoded samples before resampling/encoding. A seekable
// temporary output lets ffmpeg finalize FLAC/WAV headers (including the exact
// last-track sample count) before the existing transcoding cache serves it.
func (e *ffmpeg) transcodeSegment(ctx context.Context, opts TranscodeOptions) (io.ReadCloser, error) {
	args, err := segmentArgs(opts)
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp("", "navidrome-cue-*")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	if err = f.Close(); err != nil {
		_ = os.Remove(name)
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(name)
		}
	}()
	args = append(args[:len(args)-1], "-y", name)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// FFmpeg's quiet mode prevents unbounded stderr buffering for malformed media.
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("encoding CUE track: %w", err)
	}
	f, err = os.Open(name)
	if err != nil {
		return nil, err
	}
	keep = true
	return &segmentOutput{File: f}, nil
}

type segmentOutput struct{ *os.File }

func (f *segmentOutput) Close() error { return errors.Join(f.File.Close(), os.Remove(f.Name())) }

func segmentArgs(opts TranscodeOptions) ([]string, error) {
	seg := opts.Segment
	if seg == nil || seg.StartSample < 0 || seg.EndSample < 0 || seg.SourceRate <= 0 || opts.Offset < 0 {
		return nil, fmt.Errorf("invalid CUE segment")
	}
	if int64(opts.Offset) > (math.MaxInt64-seg.StartSample)/int64(seg.SourceRate) {
		return nil, fmt.Errorf("CUE seek offset overflows")
	}
	start := seg.StartSample + int64(opts.Offset)*int64(seg.SourceRate)
	if seg.EndSample != 0 && start >= seg.EndSample {
		return nil, fmt.Errorf("CUE seek is beyond the end of the track")
	}
	if opts.Duration > 0 && float64(opts.Offset) >= float64(opts.Duration) {
		return nil, fmt.Errorf("CUE seek is beyond the end of the track")
	}
	if !isDefaultCommand(opts.Format, opts.Command) {
		return nil, fmt.Errorf("custom transcoding commands are unsupported for CUE tracks")
	}
	if _, ok := formatOutputMap[opts.Format]; !ok {
		return nil, fmt.Errorf("unsupported CUE output format: %s", opts.Format)
	}
	opts.Offset = 0 // Seeking is relative to the track, not the album image.
	args := buildDynamicArgs(opts)
	filter := "atrim=start_sample=" + strconv.FormatInt(start, 10)
	if seg.EndSample > 0 {
		filter += ":end_sample=" + strconv.FormatInt(seg.EndSample, 10)
	}
	args = injectBeforeOutput(args, "-af", filter+",asetpts=PTS-STARTPTS")
	if opts.Format == "wav" {
		codec := "pcm_s16le"
		switch opts.BitDepth {
		case 24:
			codec = "pcm_s24le"
		case 32:
			codec = "pcm_s32le"
		}
		args = injectBeforeOutput(args, "-c:a", codec)
	}
	// Never copy album-image track numbers, embedded CUE sheets or lyrics into
	// individual downloads. Supply the virtual track's metadata instead.
	args = injectBeforeOutput(args, "-map_metadata", "-1")
	keys := make([]string, 0, len(seg.Tags))
	for key := range seg.Tags {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		args = injectBeforeOutput(args, "-metadata", key+"="+seg.Tags[key])
	}
	return args, nil
}
