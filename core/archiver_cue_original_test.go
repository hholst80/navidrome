package core

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/metadata/cue"
)

func TestOriginalCUEArchiveCollision(t *testing.T) {
	used := map[string]bool{"album/image.ape": true}
	image, sheet := originalCUEArchiveNames("Album/image.ape", "image.cue", used)
	if image != "Album (2)/image.ape" || sheet != "Album (2)/image.cue" {
		t.Fatalf("CUE pair must retain its basenames in one unique directory: %q, %q", image, sheet)
	}
	image, sheet = originalCUEArchiveNames("Album/image.ape", "image.cue", used)
	if image != "Album (3)/image.ape" || sheet != "Album (3)/image.cue" {
		t.Fatalf("second pair collides: %q, %q", image, sheet)
	}
}

func TestOriginalCUEMissingSidecarDoesNotDeliverPartialArchive(t *testing.T) {
	source := filepath.Join(t.TempDir(), "image.ape")
	if err := os.WriteFile(source, []byte("original image"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := ZipOriginalCUE(context.Background(), &model.MediaFile{Path: source}, &cue.Sidecar{Name: "missing.cue", SourceName: "image.ape"}, &out)
	if err == nil || out.Len() != 0 {
		t.Fatalf("missing sheet must fail before delivery: err=%v, bytes=%d", err, out.Len())
	}
}

func TestOriginalCUERejectsUnsafeArchivePaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "image.ape"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../outside.cue", `..\outside.cue`, "/absolute.cue", "Album/../outside.cue", "Album//sheet.cue", "Album/./sheet.cue", "C:outside.cue"} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			z := zip.NewWriter(&out)
			err := addOriginalCUEFile(z, dir, "image.ape", name)
			_ = z.Close()
			if err == nil {
				t.Fatalf("accepted unsafe archive path %q", name)
			}
		})
	}
}
