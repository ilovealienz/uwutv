package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Watched items and fetched VOD metadata are persisted so neither survives only
// in memory: metadata never changes, so re-fetching it every session is pure
// waste, and watched state is the whole point of tracking it.

func watchedPath() string { return filepath.Join(dataDir(), "watched") }

// MigrateState moves user state written by older versions out of the cache dir
// into the data dir. Safe to call on every start: it only moves files that
// exist in the old place and not the new one.
func MigrateState() {
	names := []string{
		"favourites", "cat_favourites",
		"movie_favourites", "series_favourites",
		"movie_cat_favourites", "series_cat_favourites",
		"live_hidden", "movie_hidden", "series_hidden",
		"watched",
	}
	made := false
	for _, n := range names {
		oldPath := filepath.Join(cacheDir(), n)
		newPath := filepath.Join(dataDir(), n)
		if _, err := os.Stat(oldPath); err != nil {
			continue
		}
		if _, err := os.Stat(newPath); err == nil {
			continue // already migrated; leave both alone
		}
		if !made {
			_ = os.MkdirAll(dataDir(), 0o755)
			made = true
		}
		if os.Rename(oldPath, newPath) != nil {
			// different filesystems: fall back to copy
			if b, err := os.ReadFile(oldPath); err == nil {
				if os.WriteFile(newPath, b, 0o644) == nil {
					_ = os.Remove(oldPath)
				}
			}
		}
	}
}

// ---------- watched set ----------

func loadWatched() map[int64]bool {
	m := map[int64]bool{}
	b, err := os.ReadFile(watchedPath())
	if err != nil {
		return m
	}
	for _, line := range strings.Fields(string(b)) {
		if id, err := strconv.ParseInt(line, 10, 64); err == nil {
			m[id] = true
		}
	}
	return m
}

func saveWatched(m map[int64]bool) {
	var sb strings.Builder
	for id := range m {
		sb.WriteString(strconv.FormatInt(id, 10))
		sb.WriteByte('\n')
	}
	_ = os.MkdirAll(dataDir(), 0o755)
	_ = os.WriteFile(watchedPath(), []byte(sb.String()), 0o644)
}

// ---------- metadata caches (append-only) ----------
//
// Metadata is immutable once fetched, so the cache is a JSON-lines log: each
// newly-fetched item appends a single line. A write is O(1) and independent of
// how much is already cached — no periodic full-map rewrites, and nothing is
// lost if the process is killed rather than quit cleanly. Duplicates can only
// appear if an item is re-fetched (rare, since the cache is consulted first),
// and later lines win on load.

var infoMu sync.Mutex

func movieInfoPath() string  { return filepath.Join(cacheDir(), "movie_info.jsonl") }
func seriesInfoPath() string { return filepath.Join(cacheDir(), "series_info.jsonl") }

type movieInfoLine struct {
	ID   int64      `json:"id"`
	Info *MovieInfo `json:"info"`
}

type seriesInfoLine struct {
	ID   int64       `json:"id"`
	Info *SeriesInfo `json:"info"`
}

// maxInfoBytes caps each metadata cache. Entries are ~1-2 KB, so this holds
// roughly 4-8k titles; the oldest are evicted first, which is effectively LRU
// since a title is only appended when it's viewed.
const maxInfoBytes = 8 << 20 // 8 MiB

type idOnly struct {
	ID int64 `json:"id"`
}

// loadJSONL reads an append-only cache, keeps the newest line per id, and drops
// the oldest entries once the file exceeds maxBytes. The file is rewritten only
// when something was actually deduped or evicted.
func loadJSONL(path string, maxBytes int64) [][]byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var lines [][]byte
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(ln) != "" {
			lines = append(lines, []byte(ln))
		}
	}
	// keep only the last occurrence of each id
	last := map[int64]int{}
	for i, ln := range lines {
		var e idOnly
		if json.Unmarshal(ln, &e) == nil {
			last[e.ID] = i
		}
	}
	kept := make([][]byte, 0, len(last))
	for i, ln := range lines {
		var e idOnly
		if json.Unmarshal(ln, &e) != nil {
			continue // unparseable: drop it
		}
		if last[e.ID] == i {
			kept = append(kept, ln)
		}
	}
	// evict oldest until under the cap
	var total int64
	for _, ln := range kept {
		total += int64(len(ln)) + 1
	}
	drop := 0
	for total > maxBytes && drop < len(kept) {
		total -= int64(len(kept[drop])) + 1
		drop++
	}
	kept = kept[drop:]

	if drop > 0 || len(kept) != len(lines) {
		var buf strings.Builder
		for _, ln := range kept {
			buf.Write(ln)
			buf.WriteByte('\n')
		}
		_ = os.WriteFile(path, []byte(buf.String()), 0o644)
	}
	return kept
}

// compactJSONL rewrites a cache that has grown past the cap. Call it while
// holding infoMu — it does not lock itself.
func compactJSONL(path string) { _ = loadJSONL(path, maxInfoBytes) }

func appendLine(path string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	infoMu.Lock()
	defer infoMu.Unlock()
	_ = os.MkdirAll(cacheDir(), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	// note: f is closed explicitly below so its size can be checked
	_, _ = f.Write(append(b, '\n'))
	st, statErr := f.Stat()
	_ = f.Close()
	// compact in place while still holding the lock: loadJSONL doesn't take it,
	// so no other append can interleave and get overwritten
	if statErr == nil && st.Size() > maxInfoBytes {
		compactJSONL(path)
	}
}

func AppendMovieInfo(id int64, info *MovieInfo) {
	if info != nil {
		appendLine(movieInfoPath(), movieInfoLine{ID: id, Info: info})
	}
}

func AppendSeriesInfo(id int64, info *SeriesInfo) {
	if info != nil {
		appendLine(seriesInfoPath(), seriesInfoLine{ID: id, Info: info})
	}
}

func loadMovieInfoCache() map[int64]*MovieInfo {
	m := map[int64]*MovieInfo{}
	for _, ln := range loadJSONL(movieInfoPath(), maxInfoBytes) {
		var e movieInfoLine
		if json.Unmarshal(ln, &e) == nil && e.Info != nil {
			m[e.ID] = e.Info
		}
	}
	return m
}

func loadSeriesInfoCache() map[int64]*SeriesInfo {
	m := map[int64]*SeriesInfo{}
	for _, ln := range loadJSONL(seriesInfoPath(), maxInfoBytes) {
		var e seriesInfoLine
		if json.Unmarshal(ln, &e) == nil && e.Info != nil {
			m[e.ID] = e.Info
		}
	}
	return m
}
