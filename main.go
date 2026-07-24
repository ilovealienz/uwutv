package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------- styles ----------

var (
	cAccent = lipgloss.Color("#f5c2e7")
	cMauve  = lipgloss.Color("#cba6f7")
	cBlue   = lipgloss.Color("#89b4fa")
	cGreen  = lipgloss.Color("#a6e3a1")
	cDim    = lipgloss.Color("#6c7086")
	cFg     = lipgloss.Color("#cdd6f4")

	stTitle = lipgloss.NewStyle().Foreground(cMauve).Bold(true)
	stDim   = lipgloss.NewStyle().Foreground(cDim)
	stBadge = lipgloss.NewStyle().Foreground(cMauve)
	stSel   = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	stName  = lipgloss.NewStyle().Foreground(cFg)
	stPlay  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	stRes   = lipgloss.NewStyle().Foreground(cBlue)
	stBuf   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f9e2af"))
	stNow   = lipgloss.NewStyle().Foreground(cGreen)
	stHead  = lipgloss.NewStyle().Foreground(cBlue).Bold(true)
	stItem  = lipgloss.NewStyle().Foreground(cFg)

	stPreview = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(cDim).PaddingLeft(2)
	stBar = lipgloss.NewStyle().Foreground(cFg).
		BorderStyle(lipgloss.NormalBorder()).BorderTop(true).BorderForeground(cDim).
		PaddingTop(0)
)

// ---------- list item ----------

type screen int

const (
	scCats screen = iota
	scChans
	scVars
	scSeasons
	scEpisodes
	scModes
)

type item struct {
	primary string
	badge   string
	meta    string
	filter  string
	kind    string // "cat"|"chan"|"var"|"sep"|"movie"|"series"|"season"|"episode"
	cid     string
	gidx    int
	sid     int64
	fav     bool
	status  string // watch-progress glyph, shown beside the favourite star
	ext     string // container extension (VOD)
	season  string // season key (series)
	epIdx   int    // index within the season's episode slice
}

func (i item) Title() string       { return i.primary }
func (i item) Description() string { return i.meta }
func (i item) FilterValue() string { return i.filter }

type delegate struct{}

func (delegate) Height() int                         { return 1 }
func (delegate) Spacing() int                        { return 0 }
func (delegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (delegate) Render(w io.Writer, m list.Model, index int, li list.Item) {
	it, ok := li.(item)
	if !ok {
		return
	}
	if it.kind == "sep" {
		if it.primary != "" { // labelled separator (e.g. region header)
			fmt.Fprint(w, stHead.Render("  "+it.primary))
		} else {
			fmt.Fprint(w, stDim.Render("  "+strings.Repeat("\u2500", 24)))
		}
		return
	}
	star := ""
	if it.fav {
		star = stBadge.Render("\u2605 ")
	}
	if it.status != "" {
		star += stNow.Render(it.status)
	}
	cursor := "  "
	name := stItem.Render(it.primary) // always set our own fg (avoids colour bleed)
	if index == m.Index() {
		cursor = stSel.Render("> ")
		name = stSel.Render(it.primary)
	} else {
		name = stName.Render(name)
	}
	line := cursor + star + name
	if it.badge != "" {
		line += "  " + stBadge.Render("["+it.badge+"]")
	}
	if it.meta != "" {
		line += "  " + stDim.Render("\u00b7 "+it.meta)
	}
	fmt.Fprint(w, line)
}

// ---------- model ----------

type playing struct {
	on   bool
	name string
	res  string
	sid  int64
	gen  int
}

type model struct {
	cfg       Config
	data      *Data
	player    *Player
	list      list.Model
	screen    screen
	cid       string
	clabel    string
	catIdx    int    // remembered cursor in the category list (fallback)
	catSelID  string // remembered category id
	chanIdx   int    // remembered cursor in the channel list (fallback)
	chanSelID int64  // remembered selection in the channel/series list
	seasonSel string // remembered season when descending into episodes
	groups    []Group
	curGrp    Group
	play      playing
	favs      map[int64]bool
	catFavs   map[string]bool
	epg       map[int64]epgData
	preSID    int64
	epgTok    int
	playErr   string
	buffering bool
	clock     string
	now       time.Time
	playStop  time.Time
	w, h      int
	innerW    int
	listW     int
	prevW     int
	contentH  int
	loading   bool
	status    string

	// VOD / series
	mode          uiMode
	vod           *VODData
	ser           *SeriesData
	movieInfo     map[int64]*MovieInfo
	seriesInfo    map[int64]*SeriesInfo
	movieExt      map[int64]string
	movieAdded    map[int64]time.Time
	movieFavs     map[int64]bool
	seriesFavs    map[int64]bool
	movieCatFavs  map[string]bool
	seriesCatFavs map[string]bool
	liveHidden    map[string]bool
	movieHidden   map[string]bool
	seriesHidden  map[string]bool
	showHidden    bool
	prevScreen    screen // where the mode menu was opened from
	curSeriesID   int64
	curSeriesName string
	curSeason     string
	watched       map[int64]bool
	playSeriesID  int64
	playSeason    string
	playEpIdx     int
	playIsEp      bool
	infoTok       int
	modeIdx       map[uiMode]int // remembered category cursor per mode
}

type dataMsg struct {
	data *Data
	err  error
}
type qualityMsg struct {
	gen int
	res string
}
type epgMsg struct {
	sid     int64
	entries []EPGEntry
}
type epgSettleMsg struct {
	tok int
	sid int64
}
type playErrMsg struct{ err error }
type vodDataMsg struct {
	data *VODData
	err  error
}
type seriesDataMsg struct {
	data *SeriesData
	err  error
}
type movieInfoMsg struct {
	id   int64
	info *MovieInfo
}
type seriesInfoMsg struct {
	id   int64
	info *SeriesInfo
}
type infoSettleMsg struct {
	tok  int
	id   int64
	kind string
}

// epgData caches a channel's short EPG until `fresh` (the current programme's
// end); until then the cached now/next is still accurate, so no refetch needed.
type epgData struct {
	entries []EPGEntry
	fresh   time.Time
}
type tickMsg time.Time

func initialModel(cfg Config) model {
	l := list.New(nil, delegate{}, 0, 0)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.Styles.Title = stTitle
	return model{
		cfg: cfg, player: NewPlayer(cfg.UserAgent, cfg.MpvArgs), list: l, loading: true,
		favs: loadFavs(), catFavs: loadCatFavs(), epg: map[int64]epgData{},
		movieInfo: loadMovieInfoCache(), seriesInfo: loadSeriesInfoCache(),
		watched:  loadWatched(),
		movieExt: map[int64]string{}, movieAdded: map[int64]time.Time{}, modeIdx: map[uiMode]int{},
		movieFavs: loadFavFile(movieFavPath()), seriesFavs: loadFavFile(seriesFavPath()),
		movieCatFavs: loadStrFavFile(movieCatFavPath()), seriesCatFavs: loadStrFavFile(seriesCatFavPath()),
		liveHidden: loadStrFavFile(liveHiddenPath()), movieHidden: loadStrFavFile(movieHiddenPath()),
		seriesHidden: loadStrFavFile(seriesHiddenPath()),
		clock:        time.Now().Format("15:04"),
	}
}

func (m model) Init() tea.Cmd {
	cfg := m.cfg
	load := func() tea.Msg {
		cfg.FetchServerOffset() // learn the panel's UTC offset before any EPG
		// use the on-disk cache unless it's over a day old
		if age, ok := CacheAge(); ok && age < 24*time.Hour {
			if d, ok := LoadCache(); ok {
				return dataMsg{data: d}
			}
		}
		d, err := Fetch(cfg)
		if err != nil {
			if d2, ok := LoadCache(); ok { // network failed: fall back to stale cache
				return dataMsg{data: d2}
			}
		}
		return dataMsg{data: d, err: err}
	}
	return tea.Batch(load, tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) epgCmd(sid int64) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg { return epgMsg{sid: sid, entries: cfg.ShortEPG(sid, 6)} }
}

func titleCmd(p *Player, title string) tea.Cmd {
	return func() tea.Msg { p.SetTitle(title); return nil }
}

// ---------- list builders ----------

func (m *model) setCats() {
	m.screen = scCats
	m.list.ResetFilter()
	m.list.Title = "uwutv \u2014 categories"
	if m.showHidden {
		m.list.Title += "  (showing hidden)"
	}
	var items []list.Item
	items = append(items, item{primary: "All channels", filter: "All channels", kind: "cat", cid: "__all__"})
	if len(m.favs) > 0 {
		items = append(items, item{primary: "Favourites", filter: "Favourites", kind: "cat", cid: "__fav__"})
	}
	ordered := m.data.OrderedCategories(m.cfg.Priority, m.catFavs,
		m.cfg.LiveShow, m.cfg.LiveHide, m.liveHidden, m.showHidden)
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

func (m *model) channelsFor(cid string) []Channel {
	if cid == "__fav__" {
		var out []Channel
		for _, c := range m.data.Live {
			if m.favs[c.StreamID] {
				out = append(out, c)
			}
		}
		return out
	}
	return m.data.ChannelsIn(cid)
}

func (m *model) setChans(cid, label string) {
	m.screen = scChans
	m.cid = cid
	m.clabel = label
	m.list.ResetFilter()
	m.list.Title = "uwutv \u2014 " + label

	flat := cid == "__all__"
	if flat {
		m.groups = nil
		var items []list.Item
		for _, c := range m.channelsFor(cid) {
			nm := cleanDisplay(c.Name)
			meta := m.data.CatOfStream(c.StreamID)
			items = append(items, item{primary: nm, meta: meta, filter: nm + " " + meta,
				kind: "chan", gidx: -1, sid: c.StreamID, fav: m.favs[c.StreamID]})
		}
		m.list.SetItems(items)
		m.list.ResetSelected()
		return
	}

	if cid == "__fav__" {
		m.groups = GroupChannelsMixed(m.channelsFor("__fav__"), m.data)
	} else {
		code := codeOf(m.data.catName(cid))
		m.groups = GroupChannels(m.data.ChannelsIn(cid), code)
	}
	var items []list.Item
	favView := cid == "__fav__"
	curRegion := ""
	first := true
	for idx, g := range m.groups {
		if favView && (first || g.Region != curRegion) {
			curRegion = g.Region
			first = false
			label := g.Region
			if label == "" {
				label = "OTHER"
			}
			items = append(items, item{kind: "sep", primary: label})
		}
		anyFav := false
		for _, v := range g.Variants {
			if m.favs[v.ID] {
				anyFav = true
				break
			}
		}
		if len(g.Variants) == 1 {
			nm := cleanDisplay(g.Variants[0].Clean)
			items = append(items, item{primary: nm, filter: nm,
				kind: "chan", gidx: idx, sid: g.Variants[0].ID, fav: anyFav})
		} else {
			items = append(items, item{primary: g.Base, badge: g.Badge(),
				filter: g.Base + " " + g.Badge(), kind: "chan", gidx: idx,
				sid: g.Variants[0].ID, fav: anyFav})
		}
	}
	m.list.SetItems(items)
	m.list.ResetSelected()
}

func (m *model) setVars(g Group) {
	m.screen = scVars
	m.curGrp = g
	m.list.ResetFilter()
	m.list.Title = "uwutv \u2014 " + g.Base
	labels := g.VariantLabels()
	var items []list.Item
	for i, v := range g.Variants {
		items = append(items, item{primary: labels[i], filter: labels[i],
			kind: "var", sid: v.ID, fav: m.favs[v.ID]})
	}
	m.list.SetItems(items)
	m.list.ResetSelected()
}

func (m *model) startPlay(name string, sid int64) tea.Cmd {
	gen := m.player.Play(m.cfg.LiveURL(sid), name)
	m.play = playing{on: true, name: name, sid: sid, gen: gen}
	m.playStop = time.Time{}
	m.playIsEp = false
	m.playErr = ""
	m.buffering = false
	var cmds []tea.Cmd
	if c, ok := m.epg[sid]; ok && len(c.entries) > 0 {
		if e, ok := nowEntry(c.entries, time.Now()); ok {
			m.playStop = e.Stop
			if e.Title != "" {
				cmds = append(cmds, titleCmd(m.player, name+" \u2014 "+e.Title))
			}
		}
	} else if !m.epgFresh(sid) {
		cmds = append(cmds, m.epgCmd(sid))
	}
	return tea.Batch(cmds...)
}

// ---------- update ----------

// restoreCursor selects idx (clamped), skipping a separator if it lands on one.
func (m *model) restoreCursor(idx int) {
	n := len(m.list.VisibleItems())
	if n == 0 {
		return
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	m.list.Select(idx)
	if it, ok := m.list.SelectedItem().(item); ok && it.kind == "sep" {
		m.moveCursor(1)
	}
}

// selectBySID puts the cursor back on a specific item after a list rebuild.
// Indexes shift when a filter is cleared or the list changes, so identity is
// the reliable anchor; fallback is the remembered index.
func (m *model) selectBySID(sid int64, fallback int) {
	if sid != 0 {
		for i, li := range m.list.VisibleItems() {
			if cur, ok := li.(item); ok && cur.kind != "sep" && cur.sid == sid {
				m.list.Select(i)
				return
			}
		}
	}
	m.restoreCursor(fallback)
}

// selectByCID restores the cursor onto a category after a rebuild.
func (m *model) selectByCID(cid string, fallback int) {
	if cid != "" {
		for i, li := range m.list.VisibleItems() {
			if cur, ok := li.(item); ok && cur.kind == "cat" && cur.cid == cid {
				m.list.Select(i)
				return
			}
		}
	}
	m.restoreCursor(fallback)
}

// selectBySeason is the same idea for the season list.
func (m *model) selectBySeason(season string) {
	if season != "" {
		for i, li := range m.list.VisibleItems() {
			if cur, ok := li.(item); ok && cur.kind == "season" && cur.season == season {
				m.list.Select(i)
				return
			}
		}
	}
	m.restoreCursor(0)
}

// moveCursor moves one row in dir (+1 down, -1 up) and keeps going the same
// way while the cursor sits on a separator, so separators are never selectable.
func (m *model) moveCursor(dir int) {
	n := len(m.list.VisibleItems())
	for i := 0; i < n+1; i++ {
		if dir < 0 {
			m.list.CursorUp()
		} else {
			m.list.CursorDown()
		}
		if it, ok := m.list.SelectedItem().(item); !ok || it.kind != "sep" {
			return
		}
	}
}

// hoverSchedule debounces EPG fetching: it schedules a settle tick and only the
// last one (once the cursor stops) actually fires a request, so scrolling past
// many channels costs no requests. Fresh cached EPG is reused without fetching.
func (m *model) hoverSchedule() tea.Cmd {
	it, ok := m.list.SelectedItem().(item)
	if !ok || it.kind == "sep" || it.sid == 0 {
		return nil
	}

	// VOD rows fetch item info instead of EPG, on the same debounce so
	// scrolling never fires a request per row.
	if it.kind == "movie" || it.kind == "series" {
		if it.kind == "movie" {
			if _, have := m.movieInfo[it.sid]; have {
				return nil
			}
		} else if _, have := m.seriesInfo[it.sid]; have {
			return nil
		}
		m.infoTok++
		tok, id, kind := m.infoTok, it.sid, it.kind
		return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
			return infoSettleMsg{tok: tok, id: id, kind: kind}
		})
	}
	if it.kind == "season" || it.kind == "episode" {
		return nil // already loaded with the series
	}

	sid := m.displaySID(it)
	if sid == m.preSID {
		return nil
	}
	m.preSID = sid
	if m.epgFresh(sid) {
		return nil // cached data still current — no request
	}
	m.epgTok++
	tok := m.epgTok
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return epgSettleMsg{tok: tok, sid: sid}
	})
}

// epgValidUntil is when cached EPG goes stale: the end of the current
// programme (exactly when now/next changes), or a short recheck if no data.
func epgValidUntil(entries []EPGEntry, now time.Time) time.Time {
	if len(entries) == 0 {
		return now.Add(15 * time.Minute)
	}
	for _, e := range entries {
		if !e.Stop.IsZero() && now.Before(e.Stop) {
			return e.Stop
		}
	}
	return now.Add(10 * time.Minute)
}

// nowEntry returns the programme airing at `now`, using absolute times so the
// guide's timezone never enters into it. Falls back to the first entry.
func nowEntry(entries []EPGEntry, now time.Time) (EPGEntry, bool) {
	for _, e := range entries {
		if !e.Start.IsZero() && !now.Before(e.Start) && now.Before(e.Stop) {
			return e, true
		}
	}
	if len(entries) > 0 {
		return entries[0], true
	}
	return EPGEntry{}, false
}

func (m model) epgFresh(sid int64) bool {
	c, ok := m.epg[sid]
	return ok && time.Now().Before(c.fresh)
}

// displaySID picks which stream's EPG to show for an item: for a grouped
// channel, a variant that already has guide data, else the representative.
func (m model) displaySID(it item) int64 {
	if it.kind != "chan" || it.gidx < 0 {
		return it.sid
	}
	for _, v := range m.groups[it.gidx].Variants {
		if c, ok := m.epg[v.ID]; ok && len(c.entries) > 0 {
			return v.ID
		}
	}
	return it.sid
}

func (m model) hoveredSID() int64 {
	if it, ok := m.list.SelectedItem().(item); ok && it.kind != "sep" {
		return it.sid
	}
	return 0
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		innerW := m.w - 4 // outer left/right padding of 2
		// The list only needs enough width for a channel name (~46 cols is
		// plenty); give it that and let the preview take the rest.
		lw := 46
		if innerW < 92 { // narrow terminal: split proportionally instead
			lw = innerW * 55 / 100
		}
		if lw < 24 {
			lw = 24
		}
		prev := innerW - lw
		if prev < 20 {
			prev = 20
			lw = innerW - prev
		}
		m.innerW = innerW
		m.listW, m.prevW = lw, prev
		m.contentH = m.h - 4 // top pad (1) + status bar (3)
		m.list.SetSize(lw, m.contentH)
		return m, nil

	case dataMsg:
		wasLoading := m.loading
		m.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.data = msg.data
		if wasLoading || m.screen != scChans {
			m.setCats()
		} else {
			m.setChans(m.cid, m.clabel) // refresh: keep the current category
		}
		return m, nil

	case qualityMsg:
		if msg.gen == m.play.gen {
			m.play.res = msg.res
		}
		return m, nil

	case bufferMsg:
		if msg.gen == m.play.gen {
			m.buffering = msg.buffering
		}
		return m, nil

	case epgSettleMsg:
		// only the newest settle for the still-hovered channel fires a request
		if msg.tok == m.epgTok && !m.epgFresh(msg.sid) {
			return m, m.epgCmd(msg.sid)
		}
		return m, nil

	case playErrMsg:
		m.play = playing{}
		m.buffering = false
		e := msg.err.Error()
		if strings.Contains(e, "executable file not found") {
			e = "mpv not found \u2014 is it installed?"
		}
		m.playErr = e
		return m, nil

	case epgMsg:
		m.epg[msg.sid] = epgData{entries: msg.entries, fresh: epgValidUntil(msg.entries, time.Now())}
		// playing channel: update the mpv title + track when this programme ends
		if m.play.on && msg.sid == m.play.sid && len(msg.entries) > 0 {
			if e, ok := nowEntry(msg.entries, time.Now()); ok {
				m.playStop = e.Stop
				if e.Title != "" {
					return m, titleCmd(m.player, m.play.name+" \u2014 "+e.Title)
				}
			}
		}
		// grouped channel whose variant returned no guide data: probe the next
		// untried variant, one at a time, stopping as soon as any has data.
		if len(msg.entries) == 0 {
			for _, g := range m.groups {
				if len(g.Variants) < 2 {
					continue
				}
				inGroup := false
				for _, v := range g.Variants {
					if v.ID == msg.sid {
						inGroup = true
						break
					}
				}
				if !inGroup {
					continue
				}
				for _, v := range g.Variants { // already have data somewhere? stop
					if c, ok := m.epg[v.ID]; ok && len(c.entries) > 0 {
						return m, nil
					}
				}
				for _, v := range g.Variants { // fetch next untried variant
					if _, ok := m.epg[v.ID]; !ok {
						return m, m.epgCmd(v.ID)
					}
				}
			}
		}
		return m, nil

	case vodDataMsg:
		m.loading = false
		if msg.err != nil || msg.data == nil {
			if msg.err != nil {
				m.status = msg.err.Error()
			}
			m.mode = mLive // couldn't load: stay on a mode we have data for
			m.showCats()
			return m, nil
		}
		m.vod = msg.data
		m.showCats()
		m.restoreCursor(m.modeIdx[m.mode])
		return m, m.hoverSchedule()

	case seriesDataMsg:
		m.loading = false
		if msg.err != nil || msg.data == nil {
			if msg.err != nil {
				m.status = msg.err.Error()
			}
			m.mode = mLive
			m.showCats()
			return m, nil
		}
		m.ser = msg.data
		m.showCats()
		m.restoreCursor(m.modeIdx[m.mode])
		return m, m.hoverSchedule()

	case movieInfoMsg:
		if msg.info != nil {
			m.movieInfo[msg.id] = msg.info
		}
		return m, nil

	case seriesInfoMsg:
		if msg.info != nil {
			m.seriesInfo[msg.id] = msg.info
			// if we're waiting on the season list, populate it now
			if m.screen == scSeasons && m.curSeriesID == msg.id {
				m.setSeasons(msg.id, m.curSeriesName)
			}
		}
		return m, nil

	case infoSettleMsg:
		if msg.tok != m.infoTok {
			return m, nil // superseded by a newer hover
		}
		switch msg.kind {
		case "movie":
			if _, ok := m.movieInfo[msg.id]; !ok {
				return m, m.movieInfoCmd(msg.id)
			}
		case "series":
			if _, ok := m.seriesInfo[msg.id]; !ok {
				return m, m.seriesInfoCmd(msg.id)
			}
		}
		return m, nil

	case progressMsg:
		// 90% through counts as watched — the usual convention, and it avoids
		// requiring the user to sit through end credits
		if msg.gen == m.play.gen && msg.percent >= 90 && m.play.sid != 0 {
			if !m.watched[m.play.sid] {
				m.watched[m.play.sid] = true
				saveWatched(m.watched)
				m.refreshCurrentList()
			}
		}
		return m, nil

	case eofMsg:
		if msg.gen != m.play.gen {
			return m, nil
		}
		if m.play.sid != 0 && !m.watched[m.play.sid] {
			m.watched[m.play.sid] = true
			saveWatched(m.watched)
			m.refreshCurrentList()
		}
		if m.playIsEp && m.cfg.AutoplayNext {
			if cmd, ok := m.playNextEpisode(); ok {
				return m, cmd
			}
		}
		return m, nil

	case mpvExitedMsg:
		if msg.gen == m.play.gen {
			m.play = playing{}
			m.buffering = false
		}
		return m, nil

	case tickMsg:
		prev := m.now
		m.now = time.Time(msg)
		m.clock = m.now.Format("15:04")
		cmds := []tea.Cmd{tickCmd()}
		if m.play.on && m.now.Minute() != prev.Minute() {
			min := m.now.Minute()
			// Refresh a minute *after* the usual programme boundaries (:01/:31)
			// so the provider's guide has rolled over to the new programme —
			// refreshing exactly on :00/:30 races it and grabs the old one.
			// Also refresh whenever the tracked programme's end time passes, to
			// catch shows that don't sit on the half hour.
			boundary := min == 1 || min == 31
			ended := !m.playStop.IsZero() && m.now.After(m.playStop)
			if ended {
				m.playStop = time.Time{} // fire once; fresh EPG resets it
			}
			if boundary || ended {
				cmds = append(cmds, m.epgCmd(m.play.sid))
			}
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "q", "ctrl+c":
			m.player.Stop()
			return m, tea.Quit
		case "esc":
			switch m.screen {
			case scCats:
				m.player.Stop()
				return m, tea.Quit
			case scChans:
				m.showCats()
				m.selectByCID(m.catSelID, m.catIdx)
				return m, m.hoverSchedule()
			case scVars:
				m.setChans(m.cid, m.clabel)
				m.selectBySID(m.chanSelID, m.chanIdx)
				return m, nil
			case scSeasons:
				m.setSeriesList(m.cid, m.clabel)
				m.selectBySID(m.chanSelID, m.chanIdx)
				return m, m.hoverSchedule()
			case scEpisodes:
				m.setSeasons(m.curSeriesID, m.curSeriesName)
				m.selectBySeason(m.seasonSel)
				return m, nil
			case scModes:
				if m.prevScreen == scChans {
					m.screen = scChans // rebuild the list we came from
					switch m.mode {
					case mMovies:
						m.setMovies(m.cid, m.clabel)
					case mSeries:
						m.setSeriesList(m.cid, m.clabel)
					default:
						m.setChans(m.cid, m.clabel)
					}
					m.selectBySID(m.chanSelID, m.chanIdx)
					return m, m.hoverSchedule()
				}
				m.showCats()
				m.restoreCursor(m.modeIdx[m.mode])
				return m, m.hoverSchedule()
			}
		case "up", "k":
			m.moveCursor(-1)
			return m, m.hoverSchedule()
		case "down", "j":
			m.moveCursor(1)
			return m, m.hoverSchedule()
		case "tab":
			if m.screen == scModes {
				return m, nil // already open
			}
			switch m.screen {
			case scCats:
				m.modeIdx[m.mode] = m.list.Index()
			case scChans:
				m.chanIdx = m.list.Index()
			}
			m.prevScreen = m.screen
			m.setModeMenu()
			return m, nil

		case "ctrl+r":
			cfg := m.cfg
			switch m.mode {
			case mMovies:
				return m, func() tea.Msg { d, err := FetchVOD(cfg); return vodDataMsg{data: d, err: err} }
			case mSeries:
				return m, func() tea.Msg { d, err := FetchSeries(cfg); return seriesDataMsg{data: d, err: err} }
			}
			return m, func() tea.Msg { d, err := Fetch(cfg); return dataMsg{data: d, err: err} }
		case "h":
			if m.screen != scCats {
				break
			}
			sel, ok := m.list.SelectedItem().(item)
			if !ok || sel.kind != "cat" || sel.cid == "__all__" || sel.cid == "__fav__" {
				return m, nil
			}
			switch m.mode {
			case mMovies:
				toggleStrFavFile(movieHiddenPath(), m.movieHidden, sel.cid)
			case mSeries:
				toggleStrFavFile(seriesHiddenPath(), m.seriesHidden, sel.cid)
			default:
				toggleStrFavFile(liveHiddenPath(), m.liveHidden, sel.cid)
			}
			idx := m.list.Index()
			m.showCats()
			m.restoreCursor(idx)
			return m, nil

		case "H":
			if m.screen != scCats {
				break
			}
			m.showHidden = !m.showHidden
			idx := m.list.Index()
			m.showCats()
			m.restoreCursor(idx)
			return m, nil

		case "w":
			sel, ok := m.list.SelectedItem().(item)
			if !ok || sel.kind == "sep" {
				return m, nil
			}
			if m.toggleWatched(sel) {
				switch sel.kind {
				case "season":
					season := sel.season
					m.refreshCurrentList()
					m.selectBySeason(season)
				default:
					sid, idx := sel.sid, m.list.Index()
					m.refreshCurrentList()
					m.selectBySID(sid, idx)
				}
			}
			return m, nil

		case "n":
			if cmd, ok := m.playNextEpisode(); ok {
				return m, cmd
			}
			return m, nil

		case "ctrl+f":
			return m.onFav()
		case "enter":
			return m.onEnter()
		}
	}

	var cmd tea.Cmd
	prevIdx := m.list.Index()
	m.list, cmd = m.list.Update(msg)
	// safety net for any other navigation keys (page/home/end/g/G)
	if it, ok := m.list.SelectedItem().(item); ok && it.kind == "sep" {
		if m.list.Index() < prevIdx {
			m.moveCursor(-1)
		} else {
			m.moveCursor(1)
		}
	}
	cmds := []tea.Cmd{cmd}
	if c := m.hoverSchedule(); c != nil {
		cmds = append(cmds, c)
	}
	return m, tea.Batch(cmds...)
}

func (m model) onFav() (tea.Model, tea.Cmd) {
	sel, ok := m.list.SelectedItem().(item)
	if !ok || sel.kind == "sep" {
		return m, nil
	}

	// Movies / series favourites: toggle and update the row in place.
	if sel.kind == "movie" || sel.kind == "series" {
		updated := sel
		if sel.kind == "movie" {
			toggleFavFile(movieFavPath(), m.movieFavs, sel.sid)
			updated.fav = m.movieFavs[sel.sid]
		} else {
			toggleFavFile(seriesFavPath(), m.seriesFavs, sel.sid)
			updated.fav = m.seriesFavs[sel.sid]
		}
		var cmd tea.Cmd
		for i, li := range m.list.Items() {
			if cur, ok := li.(item); ok && cur.kind == sel.kind && cur.sid == sel.sid {
				cmd = m.list.SetItem(i, updated)
				break
			}
		}
		return m, cmd
	}

	// Categories re-pin to the top when favourited, so they need a full
	// re-sort; that's fine since the row moves anyway.
	if m.screen == scCats {
		switch m.mode {
		case mMovies:
			toggleStrFavFile(movieCatFavPath(), m.movieCatFavs, sel.cid)
			m.setVODCats()
			for i, li := range m.list.VisibleItems() {
				if cur, ok := li.(item); ok && cur.kind == "cat" && cur.cid == sel.cid {
					m.list.Select(i)
					break
				}
			}
			return m, nil
		case mSeries:
			toggleStrFavFile(seriesCatFavPath(), m.seriesCatFavs, sel.cid)
			m.setSeriesCats()
			for i, li := range m.list.VisibleItems() {
				if cur, ok := li.(item); ok && cur.kind == "cat" && cur.cid == sel.cid {
					m.list.Select(i)
					break
				}
			}
			return m, nil
		}
		toggleCatFav(m.catFavs, sel.cid)
		m.setCats()
		// the row moves (fav pins to top), so re-find it by cid
		for i, li := range m.list.VisibleItems() {
			if cur, ok := li.(item); ok && cur.kind == "cat" && cur.cid == sel.cid {
				m.list.Select(i)
				break
			}
		}
		return m, nil
	}

	// Channels/variants: toggle and update just this row in place, which keeps
	// any active search filter and the cursor position intact (a full rebuild
	// would ResetFilter and drop the search).
	updated := sel
	if m.screen == scChans && sel.gidx >= 0 {
		// grouped channel: toggle ALL its variants together
		vars := m.groups[sel.gidx].Variants
		anyFav := false
		for _, v := range vars {
			if m.favs[v.ID] {
				anyFav = true
				break
			}
		}
		for _, v := range vars {
			if anyFav {
				delete(m.favs, v.ID)
			} else {
				m.favs[v.ID] = true
			}
		}
		saveFavs(m.favs)
		updated.fav = !anyFav
	} else {
		toggleFav(m.favs, sel.sid)
		updated.fav = m.favs[sel.sid]
	}

	// SetItem indexes the master slice, so find the row by identity.
	var cmd tea.Cmd
	for i, li := range m.list.Items() {
		if cur, ok := li.(item); ok && cur.kind != "sep" &&
			cur.sid == sel.sid && cur.gidx == sel.gidx {
			cmd = m.list.SetItem(i, updated)
			break
		}
	}
	return m, cmd
}

// showCats renders the category list for the current mode.
func (m *model) showCats() {
	switch m.mode {
	case mMovies:
		if m.vod != nil {
			m.setVODCats()
			return
		}
	case mSeries:
		if m.ser != nil {
			m.setSeriesCats()
			return
		}
	}
	m.setCats()
}

func (m model) seriesInfoCmd(id int64) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		info := cfg.SeriesInfo(id)
		AppendSeriesInfo(id, info) // one appended line; O(1), no rewrite
		return seriesInfoMsg{id: id, info: info}
	}
}

func (m model) movieInfoCmd(id int64) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		info := cfg.MovieInfo(id)
		AppendMovieInfo(id, info)
		return movieInfoMsg{id: id, info: info}
	}
}

// playMovie / playEpisode start VOD playback (seekable, resumable).
func (m *model) playMovie(name string, id int64) tea.Cmd {
	url := m.cfg.MovieURL(id, m.movieExt[id])
	gen := m.player.PlayKind(url, name, false)
	m.play = playing{on: true, name: name, sid: id, gen: gen}
	m.playStop = time.Time{}
	m.playErr = ""
	m.buffering = false
	m.playIsEp = false
	return nil
}

func (m *model) playEpisode(name string, id int64, ext string) tea.Cmd {
	return m.playEpisodeAt(name, id, ext, m.curSeriesID, m.curSeason, m.epIdxOf(id))
}

// epIdxOf finds an episode's index within the browsing season.
func (m *model) epIdxOf(id int64) int {
	if si := m.seriesInfo[m.curSeriesID]; si != nil {
		for i, e := range si.Episodes[m.curSeason] {
			if e.ID.Int() == id {
				return i
			}
		}
	}
	return 0
}

// playEpisodeAt starts an episode and records which one is playing, so "next"
// keeps working even after the user browses elsewhere.
func (m *model) playEpisodeAt(name string, id int64, ext string, seriesID int64, season string, idx int) tea.Cmd {
	url := m.cfg.EpisodeURL(id, ext)
	gen := m.player.PlayKind(url, name, false)
	m.play = playing{on: true, name: name, sid: id, gen: gen}
	m.playStop = time.Time{}
	m.playErr = ""
	m.buffering = false
	m.playSeriesID, m.playSeason, m.playEpIdx, m.playIsEp = seriesID, season, idx, true
	return nil
}

func (m model) onEnter() (tea.Model, tea.Cmd) {
	sel, ok := m.list.SelectedItem().(item)
	if !ok || sel.kind == "sep" {
		return m, nil
	}
	label := strings.SplitN(sel.primary, "  (", 2)[0]
	switch m.screen {
	case scModes:
		want := mLive
		switch sel.cid {
		case "movies":
			want = mMovies
		case "series":
			want = mSeries
		}
		m.mode = want
		if !m.modeReady() {
			m.loading = true
			return m, m.loadModeCmd()
		}
		m.showCats()
		m.restoreCursor(m.modeIdx[m.mode])
		return m, m.hoverSchedule()

	case scCats:
		m.catIdx, m.catSelID = m.list.Index(), sel.cid
		switch m.mode {
		case mMovies:
			m.setMovies(sel.cid, label)
		case mSeries:
			m.setSeriesList(sel.cid, label)
		default:
			m.setChans(sel.cid, label)
		}
		return m, m.hoverSchedule()

	case scChans:
		switch m.mode {
		case mMovies:
			return m, m.playMovie(sel.primary, sel.sid)
		case mSeries:
			m.chanIdx, m.chanSelID = m.list.Index(), sel.sid
			m.setSeasons(sel.sid, sel.primary)
			if m.seriesInfo[sel.sid] == nil {
				return m, m.seriesInfoCmd(sel.sid)
			}
			return m, nil
		}
		if sel.gidx < 0 {
			return m, m.startPlay(sel.primary, sel.sid)
		}
		g := m.groups[sel.gidx]
		if len(g.Variants) == 1 {
			return m, m.startPlay(cleanDisplay(g.Variants[0].Clean), g.Variants[0].ID)
		}
		m.chanIdx, m.chanSelID = m.list.Index(), sel.sid
		m.setVars(g)
		return m, nil

	case scVars:
		return m, m.startPlay(cleanDisplay(m.curGrp.Base), sel.sid)

	case scSeasons:
		m.seasonSel = sel.season
		m.setEpisodes(sel.season)
		return m, nil

	case scEpisodes:
		title := fmt.Sprintf("%s \u00b7 %s", m.curSeriesName, sel.primary)
		return m, m.playEpisode(title, sel.sid, sel.ext)
	}
	return m, nil
}

// ---------- view ----------

func lr(left, right string, width int) string {
	pad := width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right
}

func truncate(s string, n int) string {
	r := []rune(s)
	if n <= 0 {
		return ""
	}
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "\u2026"
	}
	return string(r[:n-1]) + "\u2026"
}

func (m model) View() string {
	if m.loading {
		return "\n  " + stDim.Render("loading channels\u2026")
	}
	if m.data == nil {
		return "\n  " + stDim.Render(m.status)
	}
	listView := m.list.View()
	preview := stPreview.Width(m.prevW - 1).Height(m.contentH).Render(m.previewBody())
	top := lipgloss.JoinHorizontal(lipgloss.Top, listView, preview)
	body := top + "\n" + m.statusBar()
	return lipgloss.NewStyle().Padding(1, 2, 0, 2).Render(body)
}

func (m model) previewBody() string {
	it, ok := m.list.SelectedItem().(item)
	if !ok || it.kind == "sep" {
		return stDim.Render("\u2014")
	}
	inner := m.prevW - 3 // left border + padding-left(2)
	if inner < 6 {
		inner = 6
	}
	// Movies / series / seasons / episodes have their own detail pane.
	switch it.kind {
	case "movie", "series", "season", "episode":
		return m.vodPreview(it, inner)
	case "mode":
		var b strings.Builder
		b.WriteString(stHead.Render(truncate(it.primary, inner)) + "\n\n")
		switch it.cid {
		case "live":
			b.WriteString(stDim.Render("channels with EPG"))
		case "movies":
			if m.vod != nil {
				b.WriteString(stDim.Render(fmt.Sprintf("%d movies", len(m.vod.Movies))))
			} else {
				b.WriteString(stDim.Render("not loaded yet \u00b7 enter to load"))
			}
		case "series":
			if m.ser != nil {
				b.WriteString(stDim.Render(fmt.Sprintf("%d series", len(m.ser.Series))))
			} else {
				b.WriteString(stDim.Render("not loaded yet \u00b7 enter to load"))
			}
		}
		return b.String()
	}

	var b strings.Builder
	b.WriteString(stHead.Render(truncate(it.primary, inner)) + "\n")

	// Category rows have no channel/EPG: show a short summary instead of a
	// perpetually-"loading" now/next section.
	if it.kind == "cat" {
		noun, total, favN, inCat := "channels", len(m.data.Live), len(m.favs), 0
		switch {
		case m.mode == mMovies && m.vod != nil:
			noun, total, favN = "movies", len(m.vod.Movies), len(m.movieFavs)
			if it.cid != "__all__" && it.cid != "__fav__" {
				inCat = m.vod.Counts[it.cid]
			}
		case m.mode == mSeries && m.ser != nil:
			noun, total, favN = "series", len(m.ser.Series), len(m.seriesFavs)
			if it.cid != "__all__" && it.cid != "__fav__" {
				inCat = m.ser.Counts[it.cid]
			}
		default:
			if it.cid != "__all__" && it.cid != "__fav__" {
				inCat = len(m.data.ChannelsIn(it.cid))
			}
		}
		switch it.cid {
		case "__all__":
			b.WriteString("\n" + stDim.Render(fmt.Sprintf("%d %s", total, noun)))
		case "__fav__":
			b.WriteString("\n" + stDim.Render(fmt.Sprintf("%d favourites", favN)))
		default:
			b.WriteString("\n" + stDim.Render(fmt.Sprintf("%d %s \u00b7 enter to browse", inCat, noun)))
		}
		return b.String()
	}

	if it.sid != 0 {
		if cat := m.data.CatOfStream(it.sid); cat != "" {
			b.WriteString(stDim.Render(truncate(cat, inner)) + "\n")
		}
	}
	b.WriteString("\n" + stNow.Render("now / next") + "\n")
	c, cached := m.epg[m.displaySID(it)]
	if !cached {
		b.WriteString(stDim.Render("loading\u2026"))
		return b.String()
	}
	if len(c.entries) == 0 {
		b.WriteString(stDim.Render("\u2014 no guide data \u2014"))
		return b.String()
	}
	entries := c.entries
	// prefix is: mark(2) + start(5) + gap(2) = 9 columns
	avail := inner - 15
	if avail < 4 {
		avail = 4
	}
	for i, e := range entries {
		mark := "  "
		st := stDim
		if i == 0 {
			mark = stNow.Render("\u25b6 ")
			st = lipgloss.NewStyle().Foreground(cFg)
		}
		title := truncate(e.Title, avail)
		span := ""
		if !e.Start.IsZero() {
			span = e.Start.Local().Format("15:04")
			if !e.Stop.IsZero() {
				span += "\u2013" + e.Stop.Local().Format("15:04")
			}
		}
		b.WriteString(fmt.Sprintf("%s%s  %s\n", mark, stDim.Render(span), st.Render(title)))
	}
	return b.String()
}

func (m model) statusBar() string {
	width := m.innerW
	if width < 10 {
		width = 10
	}
	clock := stBadge.Render("["+m.mode.String()+"]") + stDim.Render("  ["+m.clock+"]")
	help := stDim.Render("tab mode \u00b7 enter play \u00b7 / search \u00b7 ^f fav \u00b7 h hide \u00b7 esc back \u00b7 q quit")
	if width < 84 {
		help = stDim.Render("tab \u00b7 enter \u00b7 / \u00b7 ^f \u00b7 q")
	}

	var l1, l2 string
	if m.play.on {
		icon := "\u25b6 "
		if m.buffering {
			icon = "\u25cc " // dotted circle while rebuffering
		}
		parts := []string{stPlay.Render(icon + m.play.name)}
		if m.buffering {
			parts = append(parts, stBuf.Render("buffering\u2026"))
		} else if m.play.res != "" {
			parts = append(parts, stRes.Render(m.play.res))
		}
		l1 = strings.Join(parts, stDim.Render("  \u00b7  "))
		if c := m.epg[m.play.sid]; m.mode == mLive && len(c.entries) > 0 {
			e := c.entries
			idx := 0
			for i, x := range e { // index of the programme airing now
				if !x.Start.IsZero() && !m.now.Before(x.Start) && m.now.Before(x.Stop) {
					idx = i
					break
				}
			}
			hhmm := func(t time.Time) string {
				if t.IsZero() {
					return ""
				}
				return t.Local().Format("15:04")
			}
			l2 = stNow.Render("now ") + stDim.Render(hhmm(e[idx].Start)) + " " + e[idx].Title
			if idx+1 < len(e) {
				l2 += stDim.Render("   next ") + stDim.Render(hhmm(e[idx+1].Start)) + " " + e[idx+1].Title
			}
		}
	} else if m.playErr != "" {
		l1 = lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8")).Render("\u26a0 " + m.playErr)
	} else {
		l1 = stDim.Render("nothing playing")
	}

	line1 := lr(l1, clock, width)
	line2 := lr(l2, help, width)
	return stBar.Width(width).Render(line1 + "\n" + line2)
}

// version is set at build time: -ldflags "-X main.version=v1.2.3"
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("uwutv", version)
		return
	}
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	MigrateState() // relocate state written by older versions
	m := initialModel(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.player.SetProgram(p)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m.player.Stop()
}
