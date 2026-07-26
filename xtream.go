package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	_ "time/tzdata" // embedded zoneinfo: LoadLocation works without system tzdata

	"github.com/BurntSushi/toml"
)

// ---------- config ----------

type Config struct {
	Host           string   `toml:"host"`
	Username       string   `toml:"username"`
	Password       string   `toml:"password"`
	Ext            string   `toml:"ext"`
	UserAgent      string   `toml:"user_agent"`
	Priority       []string `toml:"priority"`
	MoviePriority  []string `toml:"movie_priority"`
	SeriesPriority []string `toml:"series_priority"`
	LiveShow       []string `toml:"live_show"`
	LiveHide       []string `toml:"live_hide"`
	MovieShow      []string `toml:"movie_show"`
	MovieHide      []string `toml:"movie_hide"`
	SeriesShow     []string `toml:"series_show"`
	SeriesHide     []string `toml:"series_hide"`
	MpvArgs        []string `toml:"mpv_args"`   // extra mpv flags, appended (override defaults)
	EpgOffset      float64  `toml:"epg_offset"` // hours; shift guide times if the panel is wrong
	AutoplayNext   bool     `toml:"autoplay_next"`
}

const configTemplate = `# uwutv configuration

host       = ""
username   = ""
password   = ""
ext        = "ts"              # "ts" or "m3u8"
user_agent = "TiviMate/4.7.0"  # some providers reject unknown clients

# Categories pinned to the top, per mode, in order. Matches the code before a
# "|" or a leading prefix ("UK" -> "UK| GENERAL", "EN -" -> "EN - WWE").
priority        = ["8K", "4K", "UK", "US", "IE"]
movie_priority  = ["EN -", "AMAZON", "APPLE+", "DISNEY+", "DREAMWORKS", "NETFLIX",
                   "PARAMOUNT", "TOP", "UNIVERSAL", "VIAPLAY"]
series_priority = ["ENGLISH", "AMAZON", "APPLE+", "CRUNCHYROLL", "DISCOVERY+",
                   "DISNEY+", "HBO MAX", "NETFLIX", "PARAMOUNT", "PEACOCK", "SKY"]

# Category filters (globs, case-insensitive). Non-empty *_show = whitelist;
# *_hide always wins.
live_show   = []
live_hide   = []
movie_show  = []
movie_hide  = []
series_show = []
series_hide = []

autoplay_next = true   # start the next episode when one ends
epg_offset    = 0.0    # shift guide by N hours if it's wrong (e.g. -1.0)

# Extra mpv flags (override the defaults). Silence live SPS log noise with:
# ["--msg-level=ffmpeg/video=no"]
mpv_args = []
`

func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "uwutv")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "uwutv")
}

// dataDir holds state that cannot be regenerated — favourites, hidden
// categories, watch history. Per the XDG spec this belongs in the data dir, not
// the cache dir: caches are meant to be safe to delete, and losing your watch
// history to a disk-cleanup script would not be.
func dataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "uwutv")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "uwutv")
}

// cacheDir holds everything re-fetchable: catalogues and item metadata.
func cacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "uwutv")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".cache", "uwutv")
}

func LoadConfig() (Config, error) {
	var c Config
	path := filepath.Join(configDir(), "config.toml")
	if p := os.Getenv("UWUTV_CONFIG"); p != "" {
		path = p
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		_ = os.WriteFile(path, []byte(configTemplate), 0o600)
		return c, fmt.Errorf("created %s - fill in your credentials, then rerun", path)
	}
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return c, err
	}
	// autoplay_next defaults to true when the key is absent (older configs)
	if !md.IsDefined("autoplay_next") {
		c.AutoplayNext = true
	}
	if v := os.Getenv("XTREAM_HOST"); v != "" {
		c.Host = v
	}
	if v := os.Getenv("XTREAM_USER"); v != "" {
		c.Username = v
	}
	if v := os.Getenv("XTREAM_PASS"); v != "" {
		c.Password = v
	}
	if v := os.Getenv("XTREAM_EXT"); v != "" {
		c.Ext = v
	}
	if v := os.Getenv("XTREAM_UA"); v != "" {
		c.UserAgent = v
	}
	if c.Ext == "" {
		c.Ext = "ts"
	}
	if c.UserAgent == "" {
		c.UserAgent = "TiviMate/4.7.0"
	}
	if c.Host == "" || c.Username == "" || c.Password == "" ||
		c.Username == "CHANGEME" || c.Password == "CHANGEME" {
		return c, fmt.Errorf("set host/username/password in %s", path)
	}
	return c, nil
}

// ---------- api ----------

type Category struct {
	ID   string `json:"category_id"`
	Name string `json:"category_name"`
}

type Channel struct {
	Name       string `json:"name"`
	StreamID   int64  `json:"stream_id"`
	CategoryID string `json:"category_id"`
}

var httpClient = &http.Client{Timeout: 20 * time.Second}

func (c Config) apiGet(action string, extra url.Values) ([]byte, error) {
	v := url.Values{}
	v.Set("username", c.Username)
	v.Set("password", c.Password)
	if action != "" {
		v.Set("action", action)
	}
	for k, vals := range extra {
		for _, x := range vals {
			v.Add(k, x)
		}
	}
	req, err := http.NewRequest("GET", c.Host+"/player_api.php?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 1<<20)
	tmp := make([]byte, 32*1024)
	for {
		n, e := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if e != nil {
			break
		}
	}
	return buf, nil
}

func (c Config) LiveURL(sid int64) string {
	return fmt.Sprintf("%s/live/%s/%s/%d.%s", c.Host, c.Username, c.Password, sid, c.Ext)
}

// ---------- server clock ----------
//
// Xtream panels publish EPG times as the *server's* wall clock, not UTC. A
// player that reads them as UTC shifts the whole guide by the server's offset
// (the classic "my EPG is 2 hours out" with panels hosted in CEST). We read the
// panel's own timezone once and parse listing times in it, so everything lands
// correctly in the user's local time — including across DST.

var (
	serverLocMu sync.RWMutex
	serverLoc   = time.UTC
	offsetKnown atomic.Bool
)

func serverLocation() *time.Location {
	serverLocMu.RLock()
	defer serverLocMu.RUnlock()
	return serverLoc
}

func setServerLocation(loc *time.Location) {
	serverLocMu.Lock()
	serverLoc = loc
	serverLocMu.Unlock()
	offsetKnown.Store(true)
}

// FetchServerOffset learns the panel's timezone. It prefers the named zone
// (authoritative, DST-aware); if that's missing or unloadable it derives a
// fixed offset from the panel's own clock fields.
func (c Config) FetchServerOffset() {
	raw, err := c.apiGet("", nil)
	if err != nil {
		return
	}
	var r struct {
		ServerInfo struct {
			Timezone     string  `json:"timezone"`
			TimestampNow flexInt `json:"timestamp_now"`
			TimeNow      string  `json:"time_now"`
		} `json:"server_info"`
	}
	_ = json.Unmarshal(raw, &r) // tolerate partial/odd payloads

	if tz := strings.TrimSpace(r.ServerInfo.Timezone); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			setServerLocation(loc)
			return
		}
	}
	// fallback: time_now is the server's wall clock, timestamp_now the epoch
	if n := r.ServerInfo.TimestampNow.Int(); n > 0 && r.ServerInfo.TimeNow != "" {
		if asUTC, err := time.Parse("2006-01-02 15:04:05", r.ServerInfo.TimeNow); err == nil {
			off := asUTC.Sub(time.Unix(n, 0).UTC())
			if off != 0 {
				setServerLocation(time.FixedZone("server", int(off.Seconds())))
			} else {
				offsetKnown.Store(true) // panel clock agrees with UTC
			}
		}
	}
}

// ---------- data + cache ----------

type Data struct {
	Cats   []Category
	Live   []Channel
	byID   map[string]string // category_id -> name
	sidCat map[int64]string  // stream_id -> category_id
}

func (d *Data) catName(id string) string     { return d.byID[id] }
func (d *Data) CatOfStream(sid int64) string { return d.byID[d.sidCat[sid]] }

func Fetch(c Config) (*Data, error) {
	catsRaw, err := c.apiGet("get_live_categories", nil)
	if err != nil {
		return nil, err
	}
	liveRaw, err := c.apiGet("get_live_streams", nil)
	if err != nil {
		return nil, err
	}
	_ = os.MkdirAll(cacheDir(), 0o755)
	_ = os.WriteFile(filepath.Join(cacheDir(), "cats.json"), catsRaw, 0o644)
	_ = os.WriteFile(filepath.Join(cacheDir(), "live.json"), liveRaw, 0o644)
	return parse(catsRaw, liveRaw)
}

// CacheAge reports how old the on-disk channel cache is.
func CacheAge() (time.Duration, bool) {
	fi, err := os.Stat(filepath.Join(cacheDir(), "live.json"))
	if err != nil {
		return 0, false
	}
	return time.Since(fi.ModTime()), true
}

func LoadCache() (*Data, bool) {
	catsRaw, e1 := os.ReadFile(filepath.Join(cacheDir(), "cats.json"))
	liveRaw, e2 := os.ReadFile(filepath.Join(cacheDir(), "live.json"))
	if e1 != nil || e2 != nil {
		return nil, false
	}
	d, err := parse(catsRaw, liveRaw)
	if err != nil {
		return nil, false
	}
	return d, true
}

func parse(catsRaw, liveRaw []byte) (*Data, error) {
	var cats []Category
	var live []Channel
	if err := json.Unmarshal(catsRaw, &cats); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(liveRaw, &live); err != nil {
		return nil, err
	}
	d := &Data{Cats: cats, Live: live, byID: map[string]string{}, sidCat: map[int64]string{}}
	for _, c := range cats {
		d.byID[c.ID] = c.Name
	}
	for _, ch := range live {
		d.sidCat[ch.StreamID] = ch.CategoryID
	}
	return d, nil
}

// ---------- name parsing / grouping ----------

var qualSet = map[string]bool{
	"HD": true, "FHD": true, "UHD": true, "SD": true, "4K": true, "8K": true,
	"HEVC": true, "H265": true, "H264": true, "RAW": true, "HQ": true,
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

func qtok(s string) string { return strings.ToUpper(nonAlnum.ReplaceAllString(s, "")) }

// matchAny reports whether name matches any glob pattern (case-insensitive,
// "*" and "?" supported, whole-string match) — e.g. "EN*", "*SPORT*", "AR ?".
func matchAny(name string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	n := strings.ToUpper(strings.TrimSpace(name))
	for _, p := range patterns {
		q := strings.ToUpper(strings.TrimSpace(p))
		if q == "" {
			continue
		}
		if ok, err := path.Match(q, n); err == nil && ok {
			return true
		}
	}
	return false
}

// catVisible applies the show/hide rules for a category. A non-empty `show`
// list acts as a whitelist; `hide` always wins; `hidden` is the interactive
// per-category hide list (revealed when showHidden is set).
func catVisible(name, id string, show, hide []string, hidden map[string]bool, showHidden bool) bool {
	if hidden[id] && !showHidden {
		return false
	}
	if len(show) > 0 && !matchAny(name, show) {
		return false
	}
	return !matchAny(name, hide)
}

// matchesPriority reports whether a category name is covered by a priority
// entry: either the code before "|" or a leading prefix. The character after
// the prefix must be non-alphanumeric so "UK" doesn't swallow "UKRAINE".
func matchesPriority(name, p string) bool {
	n := strings.ToUpper(strings.TrimSpace(name))
	q := strings.ToUpper(strings.TrimSpace(p))
	if q == "" || !strings.HasPrefix(n, q) {
		return false
	}
	if len(n) == len(q) {
		return true
	}
	c := n[len(q)]
	return !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9')
}

// priorityRank returns the index of the first matching priority entry, or -1.
func priorityRank(name string, priority []string) int {
	for i, p := range priority {
		if matchesPriority(name, p) {
			return i
		}
	}
	return -1
}

func codeOf(name string) string {
	if i := strings.Index(name, "|"); i >= 0 {
		return strings.ToUpper(strings.TrimSpace(name[:i]))
	}
	return ""
}

func stripCode(name, code string) string {
	if code == "" {
		return name
	}
	up := strings.ToUpper(name)
	for _, sep := range []string{code + ":", code + "|", code + " "} {
		if strings.HasPrefix(up, strings.ToUpper(sep)) {
			return strings.TrimSpace(name[len(sep):])
		}
	}
	return name
}

func splitQuality(name string) (string, string) {
	toks := strings.Fields(name)
	var quals, rest []string
	for _, t := range toks {
		if qualSet[qtok(t)] {
			quals = append(quals, t)
		} else {
			rest = append(rest, t)
		}
	}
	for len(rest) > 0 && qtok(rest[len(rest)-1]) == "" {
		rest = rest[:len(rest)-1]
	}
	base := strings.Join(rest, " ")
	if base == "" {
		base = name
	}
	return base, strings.Join(quals, " ")
}

func cleanDisplay(name string) string {
	toks := strings.Fields(name)
	for len(toks) > 0 && qtok(toks[len(toks)-1]) == "" {
		toks = toks[:len(toks)-1]
	}
	if len(toks) == 0 {
		return name
	}
	return strings.Join(toks, " ")
}

type Variant struct {
	Quality string
	Clean   string
	ID      int64
}

type Group struct {
	Base     string
	Region   string // category code (e.g. UK, US); "" if none
	Variants []Variant
}

func (g Group) Badge() string {
	counts := map[string]int{}
	var order []string
	for _, v := range g.Variants {
		lab := v.Quality
		if lab == "" {
			lab = "default"
		}
		if _, ok := counts[lab]; !ok {
			order = append(order, lab)
		}
		counts[lab]++
	}
	var parts []string
	for _, l := range order {
		if counts[l] > 1 {
			parts = append(parts, fmt.Sprintf("%s\u00d7%d", l, counts[l]))
		} else {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, " ")
}

// VariantLabels returns display labels with duplicate feeds disambiguated.
func (g Group) VariantLabels() []string {
	seen := map[string]int{}
	labels := make([]string, len(g.Variants))
	for i, v := range g.Variants {
		lab := v.Quality
		if lab == "" {
			lab = "default"
		}
		seen[lab]++
		if seen[lab] > 1 {
			lab = fmt.Sprintf("%s  (feed %d)", lab, seen[lab])
		}
		labels[i] = lab
	}
	return labels
}

func GroupChannels(chans []Channel, code string) []Group {
	var order []string
	m := map[string]*Group{}
	for _, c := range chans {
		clean := stripCode(c.Name, code)
		base, q := splitQuality(clean)
		g, ok := m[base]
		if !ok {
			g = &Group{Base: base}
			m[base] = g
			order = append(order, base)
		}
		g.Variants = append(g.Variants, Variant{Quality: q, Clean: clean, ID: c.StreamID})
	}
	out := make([]Group, 0, len(order))
	for _, b := range order {
		out = append(out, *m[b])
	}
	return out
}

// GroupChannelsMixed groups channels that may come from different categories,
// stripping each channel's own category code. Used for the Favourites view.
func GroupChannelsMixed(chans []Channel, d *Data) []Group {
	type key struct{ region, base string }
	var order []key
	m := map[key]*Group{}
	for _, c := range chans {
		region := codeOf(d.byID[c.CategoryID])
		clean := stripCode(c.Name, region)
		base, q := splitQuality(clean)
		k := key{region, base}
		g, ok := m[k]
		if !ok {
			g = &Group{Base: base, Region: region}
			m[k] = g
			order = append(order, k)
		}
		g.Variants = append(g.Variants, Variant{Quality: q, Clean: clean, ID: c.StreamID})
	}
	out := make([]Group, 0, len(order))
	for _, k := range order {
		out = append(out, *m[k])
	}
	// organise by region, then channel name
	sort.SliceStable(out, func(i, j int) bool {
		if !strings.EqualFold(out[i].Region, out[j].Region) {
			return strings.ToLower(out[i].Region) < strings.ToLower(out[j].Region)
		}
		return strings.ToLower(out[i].Base) < strings.ToLower(out[j].Base)
	})
	return out
}

func (d *Data) ChannelsIn(cid string) []Channel {
	if cid == "__all__" {
		return d.Live
	}
	var out []Channel
	for _, c := range d.Live {
		if c.CategoryID == cid {
			out = append(out, c)
		}
	}
	return out
}

// EPGEntry is one programme in the short EPG.
type EPGEntry struct {
	Start time.Time // absolute start
	Stop  time.Time // absolute stop
	Title string
}

// parseEPGTime prefers the Xtream epoch timestamp (unambiguous, absolute); it
// falls back to parsing the "YYYY-MM-DD HH:MM:SS" string as UTC if absent.
// parseEPGTime resolves a listing time to a true instant. The wall-clock string
// is authoritative and is read in the server's timezone; the epoch is only used
// when no string is present (and is then assumed to share the same quirk).
func parseEPGTime(ts flexInt, str string) time.Time {
	loc := serverLocation()
	if str != "" {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", str, loc); err == nil {
			return t
		}
	}
	if n := ts.Int(); n > 0 {
		t := time.Unix(n, 0).UTC()
		if offsetKnown.Load() {
			_, off := t.In(loc).Zone()
			return t.Add(-time.Duration(off) * time.Second)
		}
		return t
	}
	return time.Time{}
}

// ShortEPG returns up to `limit` upcoming programmes for a stream.
func (c Config) ShortEPG(sid int64, limit int) []EPGEntry {
	extra := url.Values{}
	extra.Set("stream_id", fmt.Sprintf("%d", sid))
	extra.Set("limit", fmt.Sprintf("%d", limit))
	raw, err := c.apiGet("get_short_epg", extra)
	if err != nil {
		return nil
	}
	var r struct {
		Listings []struct {
			Title   string  `json:"title"`
			Start   string  `json:"start"`
			End     string  `json:"end"`
			StartTS flexInt `json:"start_timestamp"`
			StopTS  flexInt `json:"stop_timestamp"`
		} `json:"epg_listings"`
	}
	// tolerate type quirks: keep whatever listings decoded rather than
	// dropping the whole guide because one field was the wrong type
	_ = json.Unmarshal(raw, &r)
	offset := time.Duration(c.EpgOffset * float64(time.Hour))
	out := make([]EPGEntry, 0, len(r.Listings))
	for _, e := range r.Listings {
		title := e.Title
		if dec, err := base64.StdEncoding.DecodeString(title); err == nil {
			title = string(dec)
		}
		out = append(out, EPGEntry{
			Start: parseEPGTime(e.StartTS, e.Start).Add(offset),
			Stop:  parseEPGTime(e.StopTS, e.End).Add(offset),
			Title: strings.TrimSpace(title),
		})
	}
	return out
}

// NowPlaying returns the title of the current programme for a stream, or "".
func (c Config) NowPlaying(sid int64) string {
	extra := url.Values{}
	extra.Set("stream_id", fmt.Sprintf("%d", sid))
	extra.Set("limit", "1")
	raw, err := c.apiGet("get_short_epg", extra)
	if err != nil {
		return ""
	}
	var r struct {
		Listings []struct {
			Title string `json:"title"`
		} `json:"epg_listings"`
	}
	if json.Unmarshal(raw, &r) != nil || len(r.Listings) == 0 {
		return ""
	}
	t := r.Listings[0].Title
	if dec, err := base64.StdEncoding.DecodeString(t); err == nil {
		return strings.TrimSpace(string(dec))
	}
	return strings.TrimSpace(t)
}

// CatEntry is one row in the category list (Sep marks a separator).
type CatEntry struct {
	Label  string
	ID     string
	Sep    bool
	Hidden bool
}

func (d *Data) OrderedCategories(priority []string, catFavs map[string]bool, show, hide []string, hidden map[string]bool, showHidden bool) []CatEntry {
	counts := map[string]int{}
	for _, c := range d.Live {
		counts[c.CategoryID]++
	}
	prioIdx := map[string]int{}
	for i, p := range priority {
		prioIdx[strings.ToUpper(p)] = i
	}
	type row struct {
		name, id string
		n        int
	}
	var favRows, rest []row
	prioBuckets := map[string][]row{}
	for id, n := range counts {
		name := d.byID[id]
		if name == "" {
			name = "?"
		}
		if !catVisible(name, id, show, hide, hidden, showHidden) {
			continue
		}
		if hidden[id] {
			name = "\u2298 " + name // only reachable while revealing hidden ones
		}
		switch {
		case catFavs[id]:
			favRows = append(favRows, row{name, id, n})
		default:
			code := codeOf(name)
			if _, ok := prioIdx[code]; ok {
				prioBuckets[code] = append(prioBuckets[code], row{name, id, n})
			} else {
				rest = append(rest, row{name, id, n})
			}
		}
	}
	sortRows := func(rs []row) {
		for i := 1; i < len(rs); i++ {
			for j := i; j > 0 && strings.ToLower(rs[j-1].name) > strings.ToLower(rs[j].name); j-- {
				rs[j-1], rs[j] = rs[j], rs[j-1]
			}
		}
	}
	sortRows(favRows)
	sortRows(rest)
	var prio []row
	for _, p := range priority {
		b := prioBuckets[strings.ToUpper(p)]
		sortRows(b)
		prio = append(prio, b...)
	}

	toEntries := func(rs []row, star bool) []CatEntry {
		es := make([]CatEntry, 0, len(rs))
		for _, r := range rs {
			label := fmt.Sprintf("%s  (%d)", r.name, r.n)
			if star {
				label = "★ " + label
			}
			es = append(es, CatEntry{Label: label, ID: r.id})
		}
		return es
	}

	groups := [][]CatEntry{toEntries(favRows, true), toEntries(prio, false), toEntries(rest, false)}
	var out []CatEntry
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, CatEntry{Sep: true})
		}
		out = append(out, g...)
	}
	return out
}
