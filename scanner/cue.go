package scanner

import (
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model"
	"github.com/navidrome/navidrome/model/metadata"
	"github.com/navidrome/navidrome/model/metadata/cue"
	"github.com/navidrome/navidrome/utils/pcmwave"
)

type cueSource struct {
	sheet    *cue.Cuesheet
	modified time.Time
	name     string
}

func (p *phaseFolders) cueSources(entry *folderEntry) map[string]cueSource {
	sources := map[string]cueSource{}
	if !conf.Server.Scanner.CUESheetSupport {
		return sources
	}
	for _, name := range slices.Sorted(maps.Keys(entry.cueFiles)) {
		f, err := entry.job.fs.Open(path.Join(entry.path, name))
		if err != nil {
			p.cueWarning(name, err)
			continue
		}
		// The parser bounds input size and rejects oversized sheets.
		sheet, err := cue.ReadCue(f)
		_ = f.Close()
		if err != nil {
			p.cueWarning(name, err)
			continue
		}
		if len(sheet.File) != 1 {
			p.cueWarning(name, fmt.Errorf("multi-file CUE sheets are unsupported"))
			continue
		}
		source := sheet.File[0].FileName
		if strings.ContainsAny(source, "/\\") {
			p.cueWarning(name, fmt.Errorf("CUE FILE must stay in the same directory"))
			continue
		}
		if _, ok := entry.audioFiles[source]; !ok {
			continue
		}
		fullPath := path.Join(entry.path, source)
		// Prefer the matching basename, then lexical order for duplicate sheets.
		preferred := strings.TrimSuffix(source, path.Ext(source)) + ".cue"
		if prev, ok := sources[fullPath]; ok && (prev.name == preferred || name != preferred) {
			continue
		}
		info, err := entry.cueFiles[name].Info()
		if err != nil {
			p.cueWarning(name, err)
			continue
		}
		sources[fullPath] = cueSource{sheet, info.ModTime(), name}
	}
	return sources
}

func (p *phaseFolders) cueWarning(name string, err error) {
	log.Warn(p.ctx, "Scanner: Skipping CUE sheet", "file", name, err)
	p.state.sendWarning(fmt.Sprintf("Skipping CUE sheet %s: %v", name, err))
}

func (p *phaseFolders) expandCUE(md metadata.Metadata, source cueSource, entry *folderEntry) model.MediaFiles {
	if !conf.Server.Scanner.CUESheetSupport {
		return nil
	}
	if source.sheet == nil {
		// WAV files may carry an embedded text CUESHEET tag.
		text := md.CUEText()
		if text == "" {
			return nil
		}
		var err error
		source.sheet, err = cue.ReadCue(strings.NewReader(text))
		if err != nil {
			p.cueWarning(md.FilePath(), err)
			return nil
		}
		if len(source.sheet.File) == 1 {
			source.sheet.File[0].FileName = path.Base(md.FilePath())
		}
	}
	var totalSamples int64
	if md.Suffix() == "wav" {
		f, err := entry.job.fs.Open(md.FilePath())
		if err != nil {
			p.cueWarning(md.FilePath(), err)
			return nil
		}
		defer f.Close()
		r, ok := f.(io.ReaderAt)
		if !ok {
			p.cueWarning(md.FilePath(), fmt.Errorf("CUE WAV source must support random access"))
			return nil
		}
		info, err := f.Stat()
		if err != nil {
			p.cueWarning(md.FilePath(), err)
			return nil
		}
		wav, err := pcmwave.Parse(r, info.Size())
		if err != nil {
			p.cueWarning(md.FilePath(), err)
			return nil
		}
		if wav.Rate != md.AudioProperties().SampleRate {
			p.cueWarning(md.FilePath(), fmt.Errorf("WAV sample rate differs from indexed metadata"))
			return nil
		}
		totalSamples = wav.Samples()
	}
	tracks, err := md.CUETracks(source.sheet, entry.job.lib.ID, entry.id, totalSamples)
	if err != nil {
		p.cueWarning(md.FilePath(), err)
		return nil
	}
	for i := range tracks {
		if source.modified.After(tracks[i].UpdatedAt) {
			tracks[i].UpdatedAt = source.modified
		}
	}
	return tracks
}
