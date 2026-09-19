package cue

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ResolveSourceName prefers the exact spelling, then a unique case-insensitive match.
func ResolveSourceName(name string, files map[string]fs.DirEntry) (string, error) {
	if _, ok := files[name]; ok {
		return name, nil
	}
	var matched string
	for candidate := range files {
		if strings.EqualFold(candidate, name) {
			if matched != "" {
				return "", fmt.Errorf("CUE FILE %q matches multiple audio files ignoring case", name)
			}
			matched = candidate
		}
	}
	return matched, nil
}

// OriginalSidecar finds the external sheet for an image using the scanner's
// basename preference and source matching. It never rewrites the sheet.
// An embedded-only image has no external original to return.
func OriginalSidecar(source string) (string, error) {
	dir, base := filepath.Dir(source), filepath.Base(source)
	entries, err := os.ReadDir(dir) // Sorted: lexical order breaks ties.
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	files := map[string]fs.DirEntry{}
	for _, entry := range entries {
		if !entry.IsDir() {
			files[entry.Name()] = entry
		}
	}
	preferred := strings.TrimSuffix(base, filepath.Ext(base)) + ".cue"
	var chosen string
	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".cue") || !entry.Type().IsRegular() {
			continue
		}
		f, err := root.Open(entry.Name())
		if err != nil {
			return "", err
		}
		sheet, err := ReadCue(f)
		_ = f.Close()
		if err != nil || len(sheet.File) != 1 || strings.ContainsAny(sheet.File[0].FileName, "/\\") {
			continue
		}
		resolved, err := ResolveSourceName(sheet.File[0].FileName, files)
		if err != nil || resolved != base {
			continue
		}
		if chosen == "" || entry.Name() == preferred {
			chosen = entry.Name()
		}
	}
	return chosen, nil
}
