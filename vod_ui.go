package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// ---------- mode ----------

type uiMode int

const (
	mLive uiMode = iota
	mMovies
	mSeries
)

func (m uiMode) String() string {
	switch m {
	case mMovies:
		return "movies"
	case mSeries:
		return "series"
	}
	return "live"
}

// modeReady reports whether the data for a mode is loaded.
func (m model) modeReady() bool {
	switch m.mode {
	case mMovies:
		return m.vod != nil
	case mSeries:
		return m.ser != nil
	}
	return m.data != nil
}

// loadModeCmd lazily loads a mode's catalogue (cache first, else fetch).
func (m model) loadModeCmd() tea.Cmd {
	cfg := m.cfg
	switch m.mode {
	case mMovies:
		return func() tea.Msg {
			if d, ok := LoadVODCache(); ok {
				return vodDataMsg{data: d}
			}
			d, err := FetchVOD(cfg)
			return vodDataMsg{data: d, err: err}
		}
	case mSeries:
		return func() tea.Msg {
			if d, ok := LoadSeriesCache(); ok {
				return seriesDataMsg{data: d}
			}
			d, err := FetchSeries(cfg)
			return seriesDataMsg{data: d, err: err}
		}
	}
	return nil
}

// ---------- list builders ----------

// orderedVODCats sorts categories: favourites first, then priority matches in
// config order, then the rest alphabetically — each block separated.
func orderedVODCats(cats []Category, counts map[string]int, favs map[string]bool, priority, show, hide []string, hidden map[string]bool, showHidden bool) []CatEntry {
	type row struct {
		name, id string
		n        int
	}
	var favRows, rest []row
	prio := make([][]row, len(priority))
	for _, c := range cats {
		n := counts[c.ID]
		if n == 0 || !catVisible(c.Name, c.ID, show, hide, hidden, showHidden) {
			continue
		}
		label := c.Name
		if hidden[c.ID] {
			label = "\u2298 " + label // shown only while revealing hidden ones
		}
		r := row{label, c.ID, n}
		switch {
		case favs[c.ID]:
			favRows = append(favRows, r)
		default:
			if k := priorityRank(c.Name, priority); k >= 0 {
				prio[k] = append(prio[k], r)
			} else {
				rest = append(rest, r)
			}
		}
	}
	byName := func(rs []row) {
		sort.SliceStable(rs, func(i, j int) bool {
			return strings.ToLower(rs[i].name) < strings.ToLower(rs[j].name)
		})
	}
	byName(favRows)
	byName(rest)
	var prioAll []row
	for i := range prio {
		byName(prio[i])
		prioAll = append(prioAll, prio[i]...)
	}

	toEntries := func(rs []row, star bool) []CatEntry {
		es := make([]CatEntry, 0, len(rs))
		for _, r := range rs {
			label := fmt.Sprintf("%s  (%d)", r.name, r.n)
			if star {
				label = "\u2605 " + label
			}
			es = append(es, CatEntry{Label: label, ID: r.id, Hidden: hidden[r.id]})
		}
		return es
	}
	var out []CatEntry
	for _, g := range [][]CatEntry{toEntries(favRows, true), toEntries(prioAll, false), toEntries(rest, false)} {
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

func (m *model) setVODCats() {
	if m.vod == nil {
		return
	}
	m.screen = scCats
	m.list.ResetFilter()
	m.list.Title = "uwutv \u2014 movies"
	if m.showHidden {
		m.list.Title += "  (showing hidden)"
	}
	var items []list.Item
	items = append(items, item{primary: "All movies", filter: "All movies", kind: "cat", cid: "__all__"})
	if len(m.movieFavs) > 0 {
		items = append(items, item{primary: "Favourites", filter: "Favourites", kind: "cat", cid: "__fav__"})
	}
	ordered := orderedVODCats(m.vod.Cats, m.vod.Counts, m.movieCatFavs, m.cfg.MoviePriority,
		m.cfg.MovieShow, m.cfg.MovieHide, m.movieHidden, m.showHidden)
	if len(ordered) > 0 {
		items = append(items, item{kind: "sep"})
	}
	for _, e := range ordered {
		if e.Sep {
			items = append(items, item{kind: "sep"})
			continue
		}
		items = append(items, item{primary: e.Label, filter: e.Label, kind: "cat", cid: e.ID})
	}
	m.list.SetItems(items)
	m.list.ResetSelected()
}

func (m *model) setSeriesCats() {
	if m.ser == nil {
		return
	}
	m.screen = scCats
	m.list.ResetFilter()
	m.list.Title = "uwutv \u2014 series"
	if m.showHidden {
		m.list.Title += "  (showing hidden)"
	}
	var items []list.Item
	items = append(items, item{primary: "All series", filter: "All series", kind: "cat", cid: "__all__"})
	if len(m.seriesFavs) > 0 {
		items = append(items, item{primary: "Favourites", filter: "Favourites", kind: "cat", cid: "__fav__"})
	}
	ordered := orderedVODCats(m.ser.Cats, m.ser.Counts, m.seriesCatFavs, m.cfg.SeriesPriority,
		m.cfg.SeriesShow, m.cfg.SeriesHide, m.seriesHidden, m.showHidden)
	if len(ordered) > 0 {
		items = append(items, item{kind: "sep"})
	}
	for _, e := range ordered {
		if e.Sep {
			items = append(items, item{kind: "sep"})
			continue
		}
		items = append(items, item{primary: e.Label, filter: e.Label, kind: "cat", cid: e.ID})
	}
	m.list.SetItems(items)
	m.list.ResetSelected()
}

// setMovies lists movies in a category (or all / favourites).
func (m *model) setMovies(cid, label string) {
	if m.vod == nil {
		return
	}
	m.screen = scChans
	m.cid = cid
	m.clabel = label
	m.list.ResetFilter()
	m.list.Title = "uwutv \u2014 " + label

	var src []Movie
	if cid == "__fav__" {
		for _, mv := range m.vod.Movies {
			if m.movieFavs[mv.StreamID.Int()] {
				src = append(src, mv)
			}
		}
	} else {
		src = m.vod.In(cid)
	}

	m.movieExt = map[int64]string{}
	m.movieAdded = map[int64]time.Time{}
	var items []list.Item
	for _, mv := range src {
		id := mv.StreamID.Int()
		m.movieExt[id] = mv.Ext
		m.movieAdded[id] = mv.AddedTime()
		nm := cleanDisplay(mv.Name)
		// the added date lives in the detail pane, not on the row — but keep it
		// searchable so "2026-07" finds recent additions
		meta := ""
		if cid == "__all__" || cid == "__fav__" {
			meta = m.vod.catName(mv.CategoryID.String())
		}
		filter := nm + " " + meta
		if t := mv.AddedTime(); !t.IsZero() {
			filter += " " + t.Local().Format("02/01/2006")
		}
		st := ""
		if m.watched[id] {
			st = glyphWatched
		}
		items = append(items, item{primary: nm, meta: meta, filter: filter,
			kind: "movie", gidx: -1, sid: id, fav: m.movieFavs[id], status: st})
	}
	m.list.SetItems(items)
	m.list.ResetSelected()
}

// setSeriesList lists series in a category (or all / favourites).
func (m *model) setSeriesList(cid, label string) {
	if m.ser == nil {
		return
	}
	m.screen = scChans
	m.cid = cid
	m.clabel = label
	m.list.ResetFilter()
	m.list.Title = "uwutv \u2014 " + label

	var src []Series
	if cid == "__fav__" {
		for _, s := range m.ser.Series {
			if m.seriesFavs[s.SeriesID.Int()] {
				src = append(src, s)
			}
		}
	} else {
		src = m.ser.In(cid)
	}

	var items []list.Item
	for _, s := range src {
		id := s.SeriesID.Int()
		nm := cleanDisplay(s.Name)
		meta := ""
		if cid == "__all__" || cid == "__fav__" {
			meta = m.ser.catName(s.CategoryID.String())
		}
		w, t, _ := m.seriesProgress(id)
		items = append(items, item{primary: nm, meta: meta, filter: nm + " " + meta,
			kind: "series", gidx: -1, sid: id, fav: m.seriesFavs[id],
			status: progressGlyph(w, t)})
	}
	m.list.SetItems(items)
	m.list.ResetSelected()
}

// setSeasons shows the season list for the selected series.
func (m *model) setSeasons(id int64, name string) {
	m.screen = scSeasons
	m.curSeriesID = id
	m.curSeriesName = name
	m.list.ResetFilter()
	m.list.Title = "uwutv \u2014 " + name

	si := m.seriesInfo[id]
	var items []list.Item
	if si == nil {
		items = append(items, item{primary: "loading\u2026", kind: "sep"})
	} else {
		for _, k := range si.Seasons() {
			label := "Season " + k
			if n, err := strconv.Atoi(k); err == nil {
				label = fmt.Sprintf("Season %d", n)
			}
			label = fmt.Sprintf("%s  (%d)", label, len(si.Episodes[k]))
			w, t := m.seasonProgress(m.curSeriesID, k)
			items = append(items, item{primary: label, filter: label,
				kind: "season", season: k, status: progressGlyph(w, t)})
		}
		if len(items) == 0 {
			items = append(items, item{primary: "no episodes found", kind: "sep"})
		}
	}
	m.list.SetItems(items)
	m.list.ResetSelected()
}

// setEpisodes shows the episodes of one season.
func (m *model) setEpisodes(season string) {
	m.screen = scEpisodes
	m.curSeason = season
	m.list.ResetFilter()
	m.list.Title = fmt.Sprintf("uwutv \u2014 %s \u00b7 S%s", m.curSeriesName, season)

	si := m.seriesInfo[m.curSeriesID]
	var items []list.Item
	if si != nil {
		for i, e := range si.Episodes[season] {
			num := e.Num.Int()
			if num == 0 {
				num = int64(i + 1)
			}
			title := strings.TrimSpace(e.Title)
			if title == "" {
				title = fmt.Sprintf("Episode %d", num)
			}
			st := ""
			if m.watched[e.ID.Int()] {
				st = glyphWatched
			}
			label := fmt.Sprintf("S%02s E%02d  \u00b7  %s", season, num, title)
			items = append(items, item{primary: label, filter: label,
				kind: "episode", sid: e.ID.Int(), ext: e.Ext, epIdx: i, status: st})
		}
	}
	if len(items) == 0 {
		items = append(items, item{primary: "no episodes", kind: "sep"})
	}
	m.list.SetItems(items)
	m.list.ResetSelected()
}

// setModeMenu shows the mode picker (tab), so switching never blind-loads.
func (m *model) setModeMenu() {
	m.screen = scModes
	m.list.ResetFilter()
	m.list.Title = "uwutv \u2014 switch to"
	mark := func(want uiMode, label string) string {
		if m.mode == want {
			return "\u25b8 " + label
		}
		return "  " + label
	}
	items := []list.Item{
		item{primary: mark(mLive, "Live TV"), filter: "live tv", kind: "mode", cid: "live"},
		item{primary: mark(mMovies, "Movies"), filter: "movies", kind: "mode", cid: "movies"},
		item{primary: mark(mSeries, "Series"), filter: "series", kind: "mode", cid: "series"},
	}
	m.list.SetItems(items)
	m.list.ResetSelected()
	switch m.mode {
	case mMovies:
		m.list.Select(1)
	case mSeries:
		m.list.Select(2)
	}
}

// refreshCurrentList rebuilds the visible list in place so watched markers
// appear immediately, preserving cursor and any active filter.
func (m *model) refreshCurrentList() {
	idx := m.list.Index()
	switch m.screen {
	case scEpisodes:
		m.setEpisodes(m.curSeason)
	case scSeasons:
		m.setSeasons(m.curSeriesID, m.curSeriesName)
	case scChans:
		switch m.mode {
		case mMovies:
			m.setMovies(m.cid, m.clabel)
		case mSeries:
			m.setSeriesList(m.cid, m.clabel)
		default:
			return
		}
	default:
		return
	}
	m.restoreCursor(idx)
}

// playNextEpisode advances within the playing season, then rolls into the next
// season. Reports false when there's nothing after the current episode.
func (m *model) playNextEpisode() (tea.Cmd, bool) {
	if !m.playIsEp || m.playSeriesID == 0 {
		return nil, false
	}
	si := m.seriesInfo[m.playSeriesID]
	if si == nil {
		return nil, false
	}
	eps := si.Episodes[m.playSeason]
	season, idx := m.playSeason, m.playEpIdx+1

	if idx >= len(eps) { // roll into the next season that has episodes
		seasons := si.Seasons()
		pos := -1
		for i, k := range seasons {
			if k == m.playSeason {
				pos = i
				break
			}
		}
		found := false
		for i := pos + 1; i >= 0 && i < len(seasons); i++ {
			if len(si.Episodes[seasons[i]]) > 0 {
				season, idx, eps = seasons[i], 0, si.Episodes[seasons[i]]
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	if idx >= len(eps) {
		return nil, false
	}

	e := eps[idx]
	num := e.Num.Int()
	if num == 0 {
		num = int64(idx + 1)
	}
	title := strings.TrimSpace(e.Title)
	if title == "" {
		title = fmt.Sprintf("Episode %d", num)
	}
	full := fmt.Sprintf("%s \u00b7 S%02s E%02d \u00b7 %s", m.curSeriesName, season, num, title)
	cmd := m.playEpisodeAt(full, e.ID.Int(), e.Ext, m.playSeriesID, season, idx)
	return cmd, true
}

// ---------- watch progress ----------

const (
	glyphWatched  = "\u2713 " // ✓ fully watched
	glyphWatching = "\u25d0 " // ◐ partially watched
)

// seriesProgress counts watched vs total episodes. Returns ok=false when the
// series hasn't been opened yet, since we can't know without its episode list.
func (m model) seriesProgress(id int64) (watched, total int, ok bool) {
	si := m.seriesInfo[id]
	if si == nil {
		return 0, 0, false
	}
	for _, eps := range si.Episodes {
		for _, e := range eps {
			total++
			if m.watched[e.ID.Int()] {
				watched++
			}
		}
	}
	return watched, total, total > 0
}

func (m model) seasonProgress(seriesID int64, season string) (watched, total int) {
	si := m.seriesInfo[seriesID]
	if si == nil {
		return 0, 0
	}
	for _, e := range si.Episodes[season] {
		total++
		if m.watched[e.ID.Int()] {
			watched++
		}
	}
	return watched, total
}

func progressGlyph(watched, total int) string {
	switch {
	case total == 0 || watched == 0:
		return ""
	case watched >= total:
		return glyphWatched
	default:
		return glyphWatching
	}
}

// episodeIDs collects episode ids for a whole series, or one season of it.
func (m model) episodeIDs(seriesID int64, season string) []int64 {
	si := m.seriesInfo[seriesID]
	if si == nil {
		return nil
	}
	var out []int64
	if season != "" {
		for _, e := range si.Episodes[season] {
			out = append(out, e.ID.Int())
		}
		return out
	}
	for _, k := range si.Seasons() {
		for _, e := range si.Episodes[k] {
			out = append(out, e.ID.Int())
		}
	}
	return out
}

// toggleWatched flips watched state for the selected row. Seasons and series
// act on every episode they contain: if anything is unwatched it marks the lot
// watched, otherwise it clears them.
func (m *model) toggleWatched(it item) bool {
	var ids []int64
	switch it.kind {
	case "movie", "episode":
		ids = []int64{it.sid}
	case "season":
		ids = m.episodeIDs(m.curSeriesID, it.season)
	case "series":
		ids = m.episodeIDs(it.sid, "")
	default:
		return false // live channels have no watched state
	}
	if len(ids) == 0 {
		return false
	}
	allWatched := true
	for _, id := range ids {
		if !m.watched[id] {
			allWatched = false
			break
		}
	}
	for _, id := range ids {
		if allWatched {
			delete(m.watched, id)
		} else {
			m.watched[id] = true
		}
	}
	saveWatched(m.watched)
	return true
}

// ---------- preview ----------

// wrapText wraps s to width, returning at most maxLines lines.
func wrapText(s string, width, maxLines int) []string {
	if width < 8 {
		width = 8
	}
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, w := range words {
		if cur == "" {
			cur = w
		} else if len([]rune(cur))+1+len([]rune(w)) <= width {
			cur += " " + w
		} else {
			lines = append(lines, cur)
			cur = w
			if len(lines) == maxLines {
				break
			}
		}
	}
	if cur != "" && len(lines) < maxLines {
		lines = append(lines, cur)
	}
	if len(lines) == maxLines && len(lines) > 0 {
		last := lines[len(lines)-1]
		if len([]rune(last)) > width-1 {
			last = string([]rune(last)[:width-1])
		}
		lines[len(lines)-1] = last + "\u2026"
	}
	return lines
}

// vodPreview renders the detail pane for a movie / series / episode row.
func (m model) vodPreview(it item, inner int) string {
	var b strings.Builder
	b.WriteString(stHead.Render(truncate(it.primary, inner)) + "\n")
	if it.meta != "" {
		b.WriteString(stDim.Render(truncate(it.meta, inner)) + "\n")
	}

	facts := func(parts ...string) {
		var keep []string
		for _, p := range parts {
			if strings.TrimSpace(p) != "" {
				keep = append(keep, p)
			}
		}
		if len(keep) > 0 {
			b.WriteString("\n" + stDim.Render(truncate(strings.Join(keep, "  \u00b7  "), inner)) + "\n")
		}
	}
	plot := func(text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		b.WriteString("\n")
		for _, ln := range wrapText(text, inner, 12) {
			b.WriteString(ln + "\n")
		}
	}

	switch it.kind {
	case "movie":
		mi := m.movieInfo[it.sid]
		if mi == nil {
			b.WriteString("\n" + stDim.Render("loading\u2026"))
			return b.String()
		}
		rating := ""
		if r := mi.Info.Rating.String(); r != "" && r != "0" {
			rating = "\u2605 " + r
		}
		added := ""
		if t := m.movieAdded[it.sid]; !t.IsZero() {
			added = "added " + t.Local().Format("02/01/2006")
		}
		facts(mi.Year(), mi.Info.Genre, mi.Info.Duration, rating, added)
		// providers often have no plot/genre for event recordings, but do have
		// probe data — show it so the pane isn't empty
		if tech := mi.Tech(); tech != "" {
			b.WriteString(stDim.Render(truncate(tech, inner)) + "\n")
		}
		plot(mi.Info.Plot)
		if mi.Info.Director != "" {
			b.WriteString("\n" + stDim.Render(truncate("dir. "+mi.Info.Director, inner)) + "\n")
		}
		if mi.Info.Cast != "" {
			b.WriteString("\n" + stDim.Render("cast") + "\n")
			for _, ln := range wrapText(mi.Info.Cast, inner, 3) {
				b.WriteString(stDim.Render(ln) + "\n")
			}
		}

	case "series":
		si := m.seriesInfo[it.sid]
		if si == nil {
			b.WriteString("\n" + stDim.Render("loading\u2026"))
			return b.String()
		}
		year := ""
		if len(si.Info.Released) >= 4 {
			year = si.Info.Released[:4]
		}
		rating := ""
		if r := si.Info.Rating.String(); r != "" && r != "0" {
			rating = "\u2605 " + r
		}
		seasons := fmt.Sprintf("%d seasons", len(si.Episodes))
		prog := ""
		if w, t, ok := m.seriesProgress(it.sid); ok && w > 0 {
			prog = fmt.Sprintf("watched %d/%d", w, t)
		}
		facts(year, si.Info.Genre, seasons, rating, prog)
		plot(si.Info.Plot)

	case "season":
		si := m.seriesInfo[m.curSeriesID]
		if si != nil {
			w, t := m.seasonProgress(m.curSeriesID, it.season)
			prog := ""
			if w > 0 {
				prog = fmt.Sprintf("watched %d/%d", w, t)
			}
			facts(fmt.Sprintf("%d episodes", len(si.Episodes[it.season])), prog)
			plot(si.Info.Plot)
		}

	case "episode":
		si := m.seriesInfo[m.curSeriesID]
		if si != nil {
			eps := si.Episodes[m.curSeason]
			if it.epIdx >= 0 && it.epIdx < len(eps) {
				e := eps[it.epIdx]
				facts(e.Info.Duration)
				plot(e.Info.Plot)
			}
		}
	}
	return b.String()
}
