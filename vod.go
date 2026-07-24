package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- tolerant JSON scalars ----------
// Xtream panels are inconsistent: the same field can be a string, a number, or
// null depending on the provider. These types accept all three.

type flexStr string

func (f *flexStr) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = ""
		return nil
	}
	if s[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return nil // tolerate junk
		}
		*f = flexStr(v)
		return nil
	}
	*f = flexStr(strings.Trim(s, `"`))
	return nil
}

func (f flexStr) String() string { return string(f) }

type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil // tolerate
	}
	*f = flexInt(n)
	return nil
}

func (f flexInt) Int() int64 { return int64(f) }

// ---------- types ----------

type Movie struct {
	Name       string  `json:"name"`
	StreamID   flexInt `json:"stream_id"`
	CategoryID flexStr `json:"category_id"`
	Ext        string  `json:"container_extension"`
	Rating     flexStr `json:"rating"`
	Added      flexStr `json:"added"`
}

// AddedTime parses the provider's "added" epoch. Zero if absent.
func (mv Movie) AddedTime() time.Time {
	if n, err := strconv.ParseInt(strings.TrimSpace(mv.Added.String()), 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0)
	}
	return time.Time{}
}

type Series struct {
	SeriesID   flexInt `json:"series_id"`
	Name       string  `json:"name"`
	CategoryID flexStr `json:"category_id"`
	Plot       string  `json:"plot"`
	Genre      string  `json:"genre"`
	Rating     flexStr `json:"rating"`
	Released   string  `json:"releaseDate"`
}

type MovieInfo struct {
	Info struct {
		Plot      string          `json:"plot"`
		Cast      string          `json:"cast"`
		Genre     string          `json:"genre"`
		Director  string          `json:"director"`
		Country   string          `json:"country"`
		Released  string          `json:"releasedate"`
		Released2 string          `json:"releaseDate"`
		Released3 string          `json:"release_date"`
		Rating    flexStr         `json:"rating"`
		Duration  string          `json:"duration"`
		Bitrate   flexInt         `json:"bitrate"`
		Video     json.RawMessage `json:"video"`
		Audio     json.RawMessage `json:"audio"`
	} `json:"info"`
	MovieData struct {
		Name string `json:"name"`
		Ext  string `json:"container_extension"`
	} `json:"movie_data"`
}

// Tech returns a technical summary ("1920×1080 · h264 · aac") from the probe
// data, which many providers populate even when plot/genre metadata is absent.
func (mi *MovieInfo) Tech() string {
	var parts []string
	var v struct {
		Codec  string `json:"codec_name"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	// these fields are an object when probed and an empty array otherwise
	if len(mi.Info.Video) > 0 && json.Unmarshal(mi.Info.Video, &v) == nil {
		if v.Width > 0 && v.Height > 0 {
			parts = append(parts, fmt.Sprintf("%d\u00d7%d", v.Width, v.Height))
		}
		if v.Codec != "" {
			parts = append(parts, v.Codec)
		}
	}
	var a struct {
		Codec    string `json:"codec_name"`
		Channels int    `json:"channels"`
	}
	if len(mi.Info.Audio) > 0 && json.Unmarshal(mi.Info.Audio, &a) == nil && a.Codec != "" {
		if a.Channels > 0 {
			parts = append(parts, fmt.Sprintf("%s %dch", a.Codec, a.Channels))
		} else {
			parts = append(parts, a.Codec)
		}
	}
	if b := mi.Info.Bitrate.Int(); b > 0 {
		parts = append(parts, fmt.Sprintf("%d kb/s", b))
	}
	return strings.Join(parts, "  \u00b7  ")
}

func (mi *MovieInfo) Year() string {
	d := mi.Info.Released
	if d == "" {
		d = mi.Info.Released2
	}
	if d == "" {
		d = mi.Info.Released3
	}
	if len(d) >= 4 {
		return d[:4]
	}
	return ""
}

type Episode struct {
	ID    flexInt `json:"id"`
	Num   flexInt `json:"episode_num"`
	Title string  `json:"title"`
	Ext   string  `json:"container_extension"`
	Info  struct {
		Plot     string `json:"plot"`
		Duration string `json:"duration"`
	} `json:"info"`
}

type SeriesInfo struct {
	Info struct {
		Plot     string  `json:"plot"`
		Cast     string  `json:"cast"`
		Genre    string  `json:"genre"`
		Released string  `json:"releaseDate"`
		Rating   flexStr `json:"rating"`
	} `json:"info"`
	Episodes map[string][]Episode
}

// Seasons returns season keys sorted numerically.
func (si *SeriesInfo) Seasons() []string {
	keys := make([]string, 0, len(si.Episodes))
	for k := range si.Episodes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, ea := strconv.Atoi(keys[i])
		b, eb := strconv.Atoi(keys[j])
		if ea == nil && eb == nil {
			return a < b
		}
		return keys[i] < keys[j]
	})
	return keys
}

// ---------- url builders ----------

func (c Config) MovieURL(id int64, ext string) string {
	if ext == "" {
		ext = "mp4"
	}
	return fmt.Sprintf("%s/movie/%s/%s/%d.%s", c.Host, c.Username, c.Password, id, ext)
}

func (c Config) EpisodeURL(id int64, ext string) string {
	if ext == "" {
		ext = "mp4"
	}
	return fmt.Sprintf("%s/series/%s/%s/%d.%s", c.Host, c.Username, c.Password, id, ext)
}

// ---------- data containers ----------

type VODData struct {
	Cats   []Category
	Movies []Movie
	byID   map[string]string
	Counts map[string]int
}

func (d *VODData) catName(id string) string { return d.byID[id] }

func (d *VODData) In(cid string) []Movie {
	if cid == "__all__" {
		return d.Movies
	}
	var out []Movie
	for _, m := range d.Movies {
		if m.CategoryID.String() == cid {
			out = append(out, m)
		}
	}
	return out
}

type SeriesData struct {
	Cats   []Category
	Series []Series
	byID   map[string]string
	Counts map[string]int
}

func (d *SeriesData) catName(id string) string { return d.byID[id] }

func (d *SeriesData) In(cid string) []Series {
	if cid == "__all__" {
		return d.Series
	}
	var out []Series
	for _, s := range d.Series {
		if s.CategoryID.String() == cid {
			out = append(out, s)
		}
	}
	return out
}

// ---------- fetching (cached to disk, same 24h policy as live) ----------

func cachePath(name string) string { return filepath.Join(cacheDir(), name) }

func FetchVOD(c Config) (*VODData, error) {
	catsRaw, err := c.apiGet("get_vod_categories", nil)
	if err != nil {
		return nil, err
	}
	listRaw, err := c.apiGet("get_vod_streams", nil)
	if err != nil {
		return nil, err
	}
	_ = os.MkdirAll(cacheDir(), 0o755)
	_ = os.WriteFile(cachePath("vodcats.json"), catsRaw, 0o644)
	_ = os.WriteFile(cachePath("vod.json"), listRaw, 0o644)
	return parseVOD(catsRaw, listRaw)
}

func LoadVODCache() (*VODData, bool) {
	a, e1 := os.ReadFile(cachePath("vodcats.json"))
	b, e2 := os.ReadFile(cachePath("vod.json"))
	if e1 != nil || e2 != nil {
		return nil, false
	}
	d, err := parseVOD(a, b)
	if err != nil {
		return nil, false
	}
	return d, true
}

func parseVOD(catsRaw, listRaw []byte) (*VODData, error) {
	var cats []Category
	var movies []Movie
	if err := json.Unmarshal(catsRaw, &cats); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(listRaw, &movies); err != nil {
		return nil, err
	}
	d := &VODData{Cats: cats, Movies: movies, byID: map[string]string{}, Counts: map[string]int{}}
	for _, c := range cats {
		d.byID[c.ID] = c.Name
	}
	for _, mv := range movies {
		d.Counts[mv.CategoryID.String()]++
	}
	sort.SliceStable(d.Movies, func(i, j int) bool {
		return strings.ToLower(d.Movies[i].Name) < strings.ToLower(d.Movies[j].Name)
	})
	return d, nil
}

func FetchSeries(c Config) (*SeriesData, error) {
	catsRaw, err := c.apiGet("get_series_categories", nil)
	if err != nil {
		return nil, err
	}
	listRaw, err := c.apiGet("get_series", nil)
	if err != nil {
		return nil, err
	}
	_ = os.MkdirAll(cacheDir(), 0o755)
	_ = os.WriteFile(cachePath("seriescats.json"), catsRaw, 0o644)
	_ = os.WriteFile(cachePath("series.json"), listRaw, 0o644)
	return parseSeries(catsRaw, listRaw)
}

func LoadSeriesCache() (*SeriesData, bool) {
	a, e1 := os.ReadFile(cachePath("seriescats.json"))
	b, e2 := os.ReadFile(cachePath("series.json"))
	if e1 != nil || e2 != nil {
		return nil, false
	}
	d, err := parseSeries(a, b)
	if err != nil {
		return nil, false
	}
	return d, true
}

func parseSeries(catsRaw, listRaw []byte) (*SeriesData, error) {
	var cats []Category
	var list []Series
	if err := json.Unmarshal(catsRaw, &cats); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(listRaw, &list); err != nil {
		return nil, err
	}
	d := &SeriesData{Cats: cats, Series: list, byID: map[string]string{}, Counts: map[string]int{}}
	for _, c := range cats {
		d.byID[c.ID] = c.Name
	}
	for _, sr := range list {
		d.Counts[sr.CategoryID.String()]++
	}
	sort.SliceStable(d.Series, func(i, j int) bool {
		return strings.ToLower(d.Series[i].Name) < strings.ToLower(d.Series[j].Name)
	})
	return d, nil
}

// ---------- per-item info (fetched lazily on hover, debounced) ----------

func (c Config) MovieInfo(id int64) *MovieInfo {
	extra := url.Values{}
	extra.Set("vod_id", fmt.Sprintf("%d", id))
	raw, err := c.apiGet("get_vod_info", extra)
	if err != nil {
		return nil
	}
	// Decode tolerantly: panels vary in field types, and a single mismatch
	// shouldn't throw away the fields that did parse.
	var mi MovieInfo
	_ = json.Unmarshal(raw, &mi)
	return &mi
}

func (c Config) SeriesInfo(id int64) *SeriesInfo {
	extra := url.Values{}
	extra.Set("series_id", fmt.Sprintf("%d", id))
	raw, err := c.apiGet("get_series_info", extra)
	if err != nil {
		return nil
	}
	// episodes is usually an object keyed by season, but some panels return an
	// array; decode the rest first, then handle episodes separately.
	var envelope struct {
		Info struct {
			Plot     string  `json:"plot"`
			Cast     string  `json:"cast"`
			Genre    string  `json:"genre"`
			Released string  `json:"releaseDate"`
			Rating   flexStr `json:"rating"`
		} `json:"info"`
		Episodes json.RawMessage `json:"episodes"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil
	}
	si := &SeriesInfo{Episodes: map[string][]Episode{}}
	si.Info = envelope.Info
	if len(envelope.Episodes) > 0 {
		var bySeason map[string][]Episode
		if json.Unmarshal(envelope.Episodes, &bySeason) == nil {
			si.Episodes = bySeason
		} else {
			var flat []Episode
			if json.Unmarshal(envelope.Episodes, &flat) == nil && len(flat) > 0 {
				si.Episodes["1"] = flat
			}
		}
	}
	return si
}
