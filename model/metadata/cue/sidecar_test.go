package cue

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalSidecar(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"embedded only", nil, ""},
		{"ape.cue", map[string]string{"album.ape.cue": "album.ape"}, "album.ape.cue"},
		{"prefer basename", map[string]string{"album.cue": "album.ape", "album.ape.cue": "album.ape"}, "album.cue"},
		{"case fallback", map[string]string{"album.cue": "ALBUM.APE"}, "album.cue"},
		{"unrelated sheet", map[string]string{"other.cue": "other.ape"}, ""},
		{"reject traversal", map[string]string{"album.cue": "../album.ape"}, ""},
		{"ambiguous case", map[string]string{"album.cue": "ALBUM.APE", "Album.ape": ""}, ""},
		{"prefer exact source", map[string]string{"album.cue": "album.ape", "Album.ape": ""}, "album.cue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("album.ape", "source")
			for name, ref := range tc.files {
				write(name, "FILE \""+ref+"\" WAVE\n  TRACK 01 AUDIO\n    INDEX 01 00:00:00\n")
			}
			got, err := OriginalSidecar(filepath.Join(dir, "album.ape"))
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
