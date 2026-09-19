package core

import (
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
