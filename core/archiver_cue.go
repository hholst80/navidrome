package core

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/metadata/cue"
	"github.com/navidrome/navidrome/utils/scanignore"
)

// ZipOriginalCUE packages the original image and external sheet without changing
// either file. It uses the same staging limits as other archive downloads.
func ZipOriginalCUE(ctx context.Context, mf *model.MediaFile, sidecar *cue.Sidecar, out io.Writer) error {
	return stageArchive(ctx, out, func(w io.Writer) error {
		z := createZipWriter(w, "raw", 0)
		for _, entry := range []struct{ source, name string }{{filepath.Base(mf.AbsolutePath()), sidecar.SourceName}, {sidecar.Name, sidecar.Name}} {
			if err := addOriginalCUEFile(z, filepath.Dir(mf.AbsolutePath()), entry.source, entry.name); err != nil {
				_ = z.Close()
				return err
			}
		}
		return z.Close()
	})
}

func addOriginalCUEFile(z *zip.Writer, dir, name, archiveName string) error {
	archiveName = filepath.ToSlash(archiveName)
	if archiveName == "." || path.IsAbs(archiveName) || path.Clean(archiveName) != archiveName || strings.ContainsAny(archiveName, "\\:") {
		return fmt.Errorf("unsafe original CUE archive name %q", archiveName)
	}
	for _, part := range strings.Split(archiveName, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("unsafe original CUE archive name %q", archiveName)
		}
	}
	// These names come from the indexed source and selected sidecar, not the request.
	// Follow file symlinks as allowed by scanning, including targets outside this folder.
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("CUE source %q is not a regular file", name)
	}
	w, err := z.CreateHeader(&zip.FileHeader{Name: filepath.ToSlash(archiveName), Method: zip.Store, Modified: info.ModTime()})
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

// Keep the CUE basename and its FILE reference intact. Resolve collisions by moving the pair into another directory.
func originalCUEArchiveNames(image, sidecar string, used map[string]bool) (string, string) {
	dir, base := filepath.Dir(image), filepath.Base(image)
	candidate := dir
	for n := 2; used[strings.ToLower(filepath.Join(candidate, base))] || used[strings.ToLower(filepath.Join(candidate, sidecar))]; n++ {
		candidate = fmt.Sprintf("%s (%d)", dir, n)
	}
	image = filepath.Join(candidate, base)
	sidecar = filepath.Join(candidate, sidecar)
	used[strings.ToLower(image)] = true
	used[strings.ToLower(sidecar)] = true
	return image, sidecar
}

// OriginalCUESidecar applies the same library-relative ignore patterns as scanning.
func OriginalCUESidecar(ctx context.Context, mf *model.MediaFile) (*cue.Sidecar, error) {
	source := mf.AbsolutePath()
	root := mf.LibraryPath
	if root == "" {
		root = filepath.Dir(source)
	}
	relativeDir, err := filepath.Rel(root, filepath.Dir(source))
	if err != nil {
		return nil, err
	}
	relativeDir = filepath.ToSlash(relativeDir)
	if relativeDir == ".." || strings.HasPrefix(relativeDir, "../") || path.IsAbs(relativeDir) {
		return nil, fmt.Errorf("CUE source is outside its library")
	}
	checker := scanignore.New(os.DirFS(root))
	if err := checker.PushAllParents(ctx, relativeDir); err != nil {
		return nil, err
	}
	return cue.OriginalSidecar(source, conf.Server.Scanner.FollowSymlinks, model.IsAudioFile, func(name string) bool {
		return checker.ShouldIgnore(ctx, path.Join(relativeDir, name))
	})
}
