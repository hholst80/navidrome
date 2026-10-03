package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os/exec"
	"strconv"
	"sync"

	"github.com/navidrome/navidrome/utils/pcmwave"
)

// ProbePCM obtains exact sample counts from the demuxer, without decoding audio.
// Unlike the library's rounded duration, these counts can define a WAV header
// and map arbitrary HTTP byte ranges back to source samples.
func ProbePCM(ctx context.Context, path string) (*pcmwave.Source, error) {
	binary, err := ffmpegCmd()
	if err != nil {
		return nil, err
	}
	data, err := exec.CommandContext(ctx, ffprobePath(binary), "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=codec_name,sample_rate,channels,bits_per_sample,bits_per_raw_sample,duration_ts,time_base,channel_layout", "-of", "json", path).Output()
	if err != nil {
		return nil, fmt.Errorf("probing CUE PCM source: %w", err)
	}
	var result struct {
		Streams []struct {
			Codec    string `json:"codec_name"`
			Rate     string `json:"sample_rate"`
			Channels int    `json:"channels"`
			Bits     int    `json:"bits_per_sample"`
			RawBits  string `json:"bits_per_raw_sample"`
			Duration int64  `json:"duration_ts"`
			TimeBase string `json:"time_base"`
			Layout   string `json:"channel_layout"`
		} `json:"streams"`
	}
	if err = json.Unmarshal(data, &result); err != nil || len(result.Streams) != 1 {
		return nil, fmt.Errorf("invalid CUE source probe")
	}
	s := result.Streams[0]
	if s.Codec != "flac" && s.Codec != "ape" {
		return nil, fmt.Errorf("CUE compressed sources must be FLAC or APE")
	}
	rate, _ := strconv.Atoi(s.Rate)
	bits := s.Bits
	if bits == 0 {
		bits, _ = strconv.Atoi(s.RawBits)
	}
	duration, ok := new(big.Rat).SetString(s.TimeBase)
	if !ok || rate <= 0 || s.Duration <= 0 {
		return nil, fmt.Errorf("CUE source has no exact sample count")
	}
	duration.Mul(duration, new(big.Rat).SetInt64(s.Duration))
	duration.Mul(duration, new(big.Rat).SetInt64(int64(rate)))
	if !duration.IsInt() || !duration.Num().IsInt64() {
		return nil, fmt.Errorf("invalid CUE source sample count")
	}
	// WAVE speaker masks preserve the decoded channel order, including FLAC's
	// custom channel layouts. Refuse unknown multichannel layouts rather than
	// silently labelling channels with different speaker positions.
	masks := map[string]uint32{
		"mono": 4, "stereo": 3, "2.1": 0xb, "3.0": 7, "3.0(back)": 0x103, "4.0": 0x107,
		"quad": 0x33, "quad(side)": 0x603, "3.1": 0xf, "5.0": 0x37, "5.0(side)": 0x607,
		"4.1": 0x10f, "5.1": 0x3f, "5.1(side)": 0x60f, "6.0": 0x707, "6.0(front)": 0x6c3,
		"hexagonal": 0x137, "6.1": 0x70f, "6.1(back)": 0x13f, "6.1(front)": 0x6cb,
		"7.0": 0x637, "7.0(front)": 0x6c7, "7.1": 0x63f, "7.1(wide)": 0xff,
		"7.1(wide-side)": 0x6cf, "octagonal": 0x737,
	}
	mask := masks[s.Layout]
	if mask == 0 && s.Channels > 2 {
		return nil, fmt.Errorf("unsupported CUE channel layout: %s", s.Layout)
	}
	return pcmwave.New(rate, s.Channels, bits, duration.Num().Int64(), mask)
}

// PCMReader provides random access to decoded PCM without caching it. Sequential
// reads share one decoder; nonsequential reads cancel it and seek a new decoder.
// HTTP HEAD and reads of the generated WAV header do not start a decoder.
// Close must be called to cancel a range that stops before the track's end.
type PCMReader struct {
	ctx      context.Context
	cancel   context.CancelFunc
	path     string
	source   *pcmwave.Source
	end      int64
	acquire  func(context.Context) (func(), error)
	mu       sync.Mutex
	position int64
	cmd      *exec.Cmd
	pipe     io.ReadCloser
	stop     context.CancelFunc
	release  func()
	stderr   bytes.Buffer
}

func NewPCMReader(ctx context.Context, path string, source *pcmwave.Source, end int64, acquire func(context.Context) (func(), error)) *PCMReader {
	ctx, cancel := context.WithCancel(ctx)
	if end == 0 {
		end = source.Samples()
	}
	return &PCMReader{ctx: ctx, cancel: cancel, path: path, source: source, end: end, acquire: acquire, position: -1}
}

func (r *PCMReader) ReadAt(p []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if off < 0 {
		return 0, fmt.Errorf("negative PCM byte offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= r.end*r.source.BlockAlign {
		return 0, io.EOF
	}
	if r.pipe == nil || r.position != off {
		r.closeDecoder()
		if err := r.start(off); err != nil {
			return 0, err
		}
	}
	n, err := io.ReadFull(r.pipe, p)
	r.position += int64(n)
	if err != nil {
		// At EOF, wait for FFmpeg to distinguish a clean end from corrupt input.
		waitErr := r.cmd.Wait()
		r.cmd = nil
		r.closeDecoder()
		if waitErr != nil {
			return n, fmt.Errorf("decoding CUE PCM: %w: %s", waitErr, r.stderr.String())
		}
		if err == io.ErrUnexpectedEOF {
			err = io.EOF
		}
	}
	return n, err
}

func (r *PCMReader) start(off int64) error {
	binary, err := ffmpegCmd()
	if err != nil {
		return err
	}
	release, err := r.acquire(r.ctx)
	if err != nil {
		return err
	}
	r.release = release
	ctx, stop := context.WithCancel(r.ctx)
	r.stop = stop
	start := off / r.source.BlockAlign
	// Seek to a whole-second point before the desired sample. Integer atrim
	// counts then select exact boundaries without decimal timestamp rounding.
	seek := max(int64(0), start/int64(r.source.Rate)-2)
	base := seek * int64(r.source.Rate)
	args := []string{"-v", "error"}
	if seek > 0 {
		args = append(args, "-ss", strconv.FormatInt(seek, 10))
	}
	args = append(args, "-i", r.path, "-map", "0:a:0", "-af", fmt.Sprintf("atrim=start_sample=%d:end_sample=%d,asetpts=PTS-STARTPTS", start-base, r.end-base))
	format := fmt.Sprintf("s%dle", r.source.Bits)
	if r.source.Bits == 8 {
		format = "u8"
	}
	args = append(args, "-c:a", "pcm_"+format, "-f", format, "pipe:1")
	cmd := exec.CommandContext(ctx, binary, args...)
	r.stderr.Reset()
	cmd.Stderr = &limitedWriter{buf: &r.stderr, limit: 4096}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		r.closeDecoder()
		return err
	}
	if err = cmd.Start(); err != nil {
		_ = pipe.Close()
		r.closeDecoder()
		return err
	}
	r.cmd, r.pipe, r.position = cmd, pipe, off
	if extra := off % r.source.BlockAlign; extra > 0 {
		if _, err = io.CopyN(io.Discard, pipe, extra); err != nil {
			r.closeDecoder()
			return err
		}
	}
	return nil
}

func (r *PCMReader) closeDecoder() {
	if r.stop != nil {
		r.stop()
		r.stop = nil
	}
	if r.pipe != nil {
		_ = r.pipe.Close()
		r.pipe = nil
	}
	if r.cmd != nil {
		_ = r.cmd.Wait()
		r.cmd = nil
	}
	if r.release != nil {
		r.release()
		r.release = nil
	}
}
func (r *PCMReader) Close() error {
	r.cancel() // Cancel before taking the lock, so a blocked ReadAt can finish.
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeDecoder()
	return nil
}
