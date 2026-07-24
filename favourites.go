package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func favPath() string          { return filepath.Join(dataDir(), "favourites") }
func movieFavPath() string     { return filepath.Join(dataDir(), "movie_favourites") }
func seriesFavPath() string    { return filepath.Join(dataDir(), "series_favourites") }
func movieCatFavPath() string  { return filepath.Join(dataDir(), "movie_cat_favourites") }
func seriesCatFavPath() string { return filepath.Join(dataDir(), "series_cat_favourites") }
func liveHiddenPath() string   { return filepath.Join(dataDir(), "live_hidden") }
func movieHiddenPath() string  { return filepath.Join(dataDir(), "movie_hidden") }
func seriesHiddenPath() string { return filepath.Join(dataDir(), "series_hidden") }

// string-set variants, for category favourites in the VOD modes
func loadStrFavFile(path string) map[string]bool {
	m := map[string]bool{}
	b, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	for _, line := range strings.Fields(string(b)) {
		m[line] = true
	}
	return m
}

func toggleStrFavFile(path string, m map[string]bool, id string) {
	if id == "" || id == "__all__" || id == "__fav__" {
		return
	}
	if m[id] {
		delete(m, id)
	} else {
		m[id] = true
	}
	var sb strings.Builder
	for k := range m {
		sb.WriteString(k)
		sb.WriteByte('\n')
	}
	_ = os.MkdirAll(dataDir(), 0o755)
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

// loadFavFile / saveFavFile are the generic int-set helpers used by the movie
// and series favourite lists (live favourites keep their own legacy file).
func loadFavFile(path string) map[int64]bool {
	m := map[int64]bool{}
	b, err := os.ReadFile(path)
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

func saveFavFile(path string, m map[int64]bool) {
	var sb strings.Builder
	for id := range m {
		sb.WriteString(strconv.FormatInt(id, 10))
		sb.WriteByte('\n')
	}
	_ = os.MkdirAll(dataDir(), 0o755)
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func toggleFavFile(path string, m map[int64]bool, id int64) {
	if m[id] {
		delete(m, id)
	} else {
		m[id] = true
	}
	saveFavFile(path, m)
}
func catFavPath() string { return filepath.Join(dataDir(), "cat_favourites") }

func loadFavs() map[int64]bool {
	m := map[int64]bool{}
	b, err := os.ReadFile(favPath())
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

func saveFavs(m map[int64]bool) {
	var sb strings.Builder
	for id := range m {
		sb.WriteString(strconv.FormatInt(id, 10))
		sb.WriteByte('\n')
	}
	_ = os.MkdirAll(dataDir(), 0o755)
	_ = os.WriteFile(favPath(), []byte(sb.String()), 0o644)
}

func toggleFav(m map[int64]bool, id int64) {
	if m[id] {
		delete(m, id)
	} else {
		m[id] = true
	}
	saveFavs(m)
}

func loadCatFavs() map[string]bool {
	m := map[string]bool{}
	b, err := os.ReadFile(catFavPath())
	if err != nil {
		return m
	}
	for _, line := range strings.Fields(string(b)) {
		m[line] = true
	}
	return m
}

func saveCatFavs(m map[string]bool) {
	var sb strings.Builder
	for id := range m {
		sb.WriteString(id)
		sb.WriteByte('\n')
	}
	_ = os.MkdirAll(dataDir(), 0o755)
	_ = os.WriteFile(catFavPath(), []byte(sb.String()), 0o644)
}

func toggleCatFav(m map[string]bool, id string) {
	if id == "" || id == "__all__" || id == "__fav__" {
		return
	}
	if m[id] {
		delete(m, id)
	} else {
		m[id] = true
	}
	saveCatFavs(m)
}
