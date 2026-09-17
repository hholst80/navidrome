package core

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

type failedArchiveWriter struct{ err error }

func (w failedArchiveWriter) Write([]byte) (int, error) { return 0, w.err }

func TestArchiveStaging(t *testing.T) {
	failure := errors.New("test failure")
	for _, mode := range []string{"success", "generation failure", "delivery failure"} {
		t.Run(mode, func(t *testing.T) {
			var output bytes.Buffer
			var destination io.Writer = &output
			if mode == "delivery failure" {
				destination = failedArchiveWriter{failure}
			}
			var name string
			err := stageArchive(destination, func(w io.Writer) error {
				name = w.(*os.File).Name()
				if _, err := io.WriteString(w, "archive contents"); err != nil {
					return err
				}
				if mode == "generation failure" {
					return failure
				}
				return nil
			})
			if _, statErr := os.Stat(name); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("temporary archive was not removed: %v", statErr)
			}
			if mode == "success" {
				if err != nil || output.String() != "archive contents" {
					t.Fatalf("delivery: output=%q err=%v", output.String(), err)
				}
			} else if !errors.Is(err, failure) || output.Len() != 0 {
				t.Fatalf("failure: output=%q err=%v", output.String(), err)
			}
			if errors.Is(err, ErrArchiveDelivery) != (mode == "delivery failure") {
				t.Fatalf("incorrect delivery failure classification: %v", err)
			}
		})
	}
}
