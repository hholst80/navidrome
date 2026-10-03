package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
)

type failedArchiveWriter struct{ err error }

func (w failedArchiveWriter) Write([]byte) (int, error) { return 0, w.err }

func TestArchiveStreaming(t *testing.T) {
	failure := errors.New("test failure")
	for _, mode := range []string{"success", "generation failure", "delivery failure"} {
		t.Run(mode, func(t *testing.T) {
			var output bytes.Buffer
			var destination io.Writer = &output
			if mode == "delivery failure" {
				destination = failedArchiveWriter{failure}
			}
			t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
			err := streamArchive(context.Background(), destination, func(w io.Writer) error {
				if _, err := io.WriteString(w, "archive contents"); err != nil {
					return err
				}
				if mode == "generation failure" {
					return failure
				}
				return nil
			})

			if mode == "success" {
				if err != nil || output.String() != "archive contents" {
					t.Fatalf("delivery: output=%q err=%v", output.String(), err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("failure: output=%q err=%v", output.String(), err)
			}
			if errors.Is(err, ErrArchiveDelivery) != (mode != "success") {
				t.Fatalf("incorrect delivery failure classification: %v", err)
			}
		})
	}
}

func TestArchiveCancellation(t *testing.T) {
	t.Run("cancel during generation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var out bytes.Buffer
		err := streamArchive(ctx, &out, func(w io.Writer) error {
			if _, err := w.Write([]byte("first track")); err != nil {
				return err
			}
			cancel()
			_, err := w.Write([]byte("second track"))
			return err
		})
		if !errors.Is(err, context.Canceled) || out.String() != "first track" || !errors.Is(err, ErrArchiveDelivery) {
			t.Fatalf("output=%q err=%v", out.String(), err)
		}
	})
}

func TestArchiveStreamingHasNoMaterializationLimits(t *testing.T) {
	var nested func(int) error
	nested = func(n int) error {
		return streamArchive(t.Context(), io.Discard, func(w io.Writer) error {
			if n > 0 {
				return nested(n - 1)
			}
			// Reuse one buffer to exercise actual writes beyond the previous 2 GiB cap.
			buf := make([]byte, 1<<20)
			for range 2049 {
				if _, err := w.Write(buf); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if err := nested(4); err != nil {
		t.Fatal(err)
	}
}
