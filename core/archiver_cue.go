package core

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/navidrome/navidrome/model"
)

// ZipOriginalCUE packages the original image and external sheet without changing
// either file. It uses the same staging limits as other archive downloads.
func ZipOriginalCUE(ctx context.Context, mf *model.MediaFile, sidecar string, out io.Writer) error {
	return stageArchive(ctx, out, func(w io.Writer) error {
		z := createZipWriter(w, "raw", 0)
		for _, name := range []string{filepath.Base(mf.AbsolutePath()), sidecar} {
			if err := addOriginalCUEFile(z, filepath.Dir(mf.AbsolutePath()), name, name); err != nil {
				_ = z.Close()
				return err
			}
		}
		return z.Close()
	})
}

func addOriginalCUEFile(z *zip.Writer, dir, name, archiveName string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := root.Open(name)
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

// Keep both basenames intact: renaming the image would break the unchanged CUE
// FILE reference. Resolve collisions by moving the pair into another directory.
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
