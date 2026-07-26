package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// messages pushed to the UI from the player's IPC connection
type mpvExitedMsg struct{ gen int }
type bufferMsg struct {
	gen       int
	buffering bool
}

// progressMsg carries playback position (sampled via observe_property, so it
// arrives only when it actually changes — no polling).
type progressMsg struct {
	gen     int
	percent float64
}

// eofMsg fires when a file finishes naturally (VOD runs with --keep-open=yes
// so mpv stays alive and we can distinguish "ended" from "user closed it").
type eofMsg struct{ gen int }

// Player keeps a single persistent mpv. Switching channels replaces the stream
// in-place via IPC (loadfile). A long-lived connection observes mpv properties
// so resolution/codec/fps and buffering state are *pushed* to us instead of
// polled, and a watchdog reconnects the stream if it stalls on the cache.
type Player struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	sock      string
	gen       int
	running   bool
	ua        string
	extra     []string
	prog      *tea.Program
	lastURL   string
	lastTitle string
	buffering bool
	bufSince  time.Time
	lastRecon time.Time
	live      bool
}

func NewPlayer(ua string, extra []string) *Player { return &Player{ua: ua, extra: extra} }
func (p *Player) SetProgram(t *tea.Program)       { p.prog = t }

// streamArgs are mpv flags tuned for live MPEG-TS over HTTP.
//
// reconnect_at_eof is the important one for live TV: a continuous feed that
// ffmpeg misreads as EOF (a known issue) is reconnected at the ffmpeg layer,
// seamlessly — no reload, no window blink, no pause. VOD deliberately omits it,
// since there an EOF really is the end of the file.
func streamArgs() []string {
	return []string{
		"--cache=yes",
		"--demuxer-max-bytes=64MiB",
		"--demuxer-readahead-secs=20",
		"--cache-pause=yes",
		"--cache-pause-initial=yes",
		"--cache-pause-wait=1",
		"--network-timeout=10",
		"--stream-lavf-o=reconnect=1,reconnect_at_eof=1,reconnect_streamed=1,reconnect_on_network_error=1,reconnect_delay_max=5",
		"--demuxer-lavf-o=fflags=+discardcorrupt",
		"--no-resume-playback",
	}
}

// vodArgs suit on-demand files: seekable, resumable, no live-edge tuning.
func vodArgs() []string {
	return []string{
		"--cache=yes",
		"--demuxer-max-bytes=64MiB",
		"--save-position-on-quit",
		"--keep-open=yes",
		"--network-timeout=10",
		"--stream-lavf-o=reconnect=1,reconnect_streamed=1,reconnect_delay_max=5",
	}
}

// Play starts a live stream (kept for existing call sites).
func (p *Player) Play(url, title string) int { return p.PlayKind(url, title, true) }

// PlayKind starts a stream; live selects the MPEG-TS tuning, otherwise VOD
// behaviour (seeking + resume) is applied.
func (p *Player) PlayKind(url, title string, live bool) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gen++
	gen := p.gen
	p.lastURL = url
	p.lastTitle = title
	p.buffering = false
	p.live = live

	if p.running && p.cmd != nil {
		// reuse: switch the relevant options at runtime, then replace the
		// stream in place. Observed properties fire for the new file, so the
		// quality readout updates itself.
		if live {
			p.sendLocked([]interface{}{"set_property", "resume-playback", false})
			p.sendLocked([]interface{}{"set_property", "save-position-on-quit", false})
		} else {
			p.sendLocked([]interface{}{"set_property", "resume-playback", true})
			p.sendLocked([]interface{}{"set_property", "save-position-on-quit", true})
		}
		p.sendLocked([]interface{}{"loadfile", url, "replace"})
		p.sendLocked([]interface{}{"set_property", "force-media-title", title})
		return gen
	}

	p.sock = fmt.Sprintf("/tmp/uwutv-mpv-%d.sock", os.Getpid())
	_ = os.Remove(p.sock)
	args := []string{
		"--no-terminal",
		"--force-window=immediate",
		"--input-ipc-server=" + p.sock,
		"--user-agent=" + p.ua,
		"--force-media-title=" + title,
	}
	if live {
		args = append(args, streamArgs()...)
	} else {
		args = append(args, vodArgs()...)
	}
	args = append(args, p.extra...)
	args = append(args, url)
	cmd := exec.Command("mpv", args...)
	if err := cmd.Start(); err != nil {
		if p.prog != nil {
			p.prog.Send(playErrMsg{err: err})
		}
		return gen
	}
	p.cmd = cmd
	p.running = true
	sock := p.sock

	go p.observe(sock) // persistent property observer
	go p.watchdog()    // stall -> reconnect
	go func(c *exec.Cmd) {
		_ = c.Wait()
		p.mu.Lock()
		p.running = false
		p.cmd = nil
		g := p.gen
		prog := p.prog
		p.mu.Unlock()
		if prog != nil {
			prog.Send(mpvExitedMsg{gen: g})
		}
	}(cmd)
	return gen
}

type mpvEvent struct {
	Event string          `json:"event"`
	Name  string          `json:"name"`
	Data  json.RawMessage `json:"data"`
}

func numOrZero(d json.RawMessage) float64 {
	var f float64
	if err := json.Unmarshal(d, &f); err != nil {
		return 0
	}
	return f
}

// observe holds one connection open for the life of the mpv process, registers
// property observers, and pushes qualityMsg / bufferMsg as things change.
func (p *Player) observe(sock string) {
	var conn net.Conn
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", sock); err == nil {
			conn = c
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if conn == nil {
		return
	}
	defer conn.Close()

	props := []string{"width", "height", "video-format", "container-fps",
		"paused-for-cache", "percent-pos", "eof-reached"}
	for i, pr := range props {
		req, _ := json.Marshal(map[string]interface{}{
			"command": []interface{}{"observe_property", i + 1, pr}})
		if _, err := conn.Write(append(req, '\n')); err != nil {
			return
		}
	}

	r := bufio.NewReader(conn)
	var w, h, fps float64
	var codec string

	emitQuality := func() {
		if w <= 0 || h <= 0 {
			return
		}
		res := fmt.Sprintf("%d\u00d7%d", int(w), int(h))
		if codec != "" {
			res += "  \u00b7 " + codec
		}
		if fps > 0 {
			res += "  \u00b7 " + strconv.Itoa(int(fps+0.5)) + " fps"
		}
		p.mu.Lock()
		gen := p.gen
		prog := p.prog
		p.mu.Unlock()
		if prog != nil {
			prog.Send(qualityMsg{gen: gen, res: res})
		}
	}

	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var ev mpvEvent
			if json.Unmarshal(line, &ev) == nil && ev.Event == "property-change" {
				switch ev.Name {
				case "width":
					w = numOrZero(ev.Data)
					emitQuality()
				case "height":
					h = numOrZero(ev.Data)
					emitQuality()
				case "container-fps":
					fps = numOrZero(ev.Data)
					emitQuality()
				case "video-format":
					codec = ""
					_ = json.Unmarshal(ev.Data, &codec)
					emitQuality()
				case "paused-for-cache":
					var b bool
					_ = json.Unmarshal(ev.Data, &b)
					p.setBuffering(b)
				case "percent-pos":
					if pct := numOrZero(ev.Data); pct > 0 {
						p.mu.Lock()
						gen, prog := p.gen, p.prog
						p.mu.Unlock()
						if prog != nil {
							prog.Send(progressMsg{gen: gen, percent: pct})
						}
					}
				case "eof-reached":
					var b bool
					if json.Unmarshal(ev.Data, &b) == nil && b {
						p.mu.Lock()
						gen, prog := p.gen, p.prog
						p.mu.Unlock()
						if prog != nil {
							prog.Send(eofMsg{gen: gen})
						}
					}
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *Player) setBuffering(b bool) {
	p.mu.Lock()
	if b && !p.buffering {
		p.bufSince = time.Now()
	}
	p.buffering = b
	gen := p.gen
	prog := p.prog
	p.mu.Unlock()
	if prog != nil {
		prog.Send(bufferMsg{gen: gen, buffering: b})
	}
}

// watchdog re-issues loadfile if the stream sits buffering too long, with a
// cooldown so it can't reconnect-storm.
func (p *Player) watchdog() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for range t.C {
		p.mu.Lock()
		running := p.running
		stalled := p.live && p.buffering && time.Since(p.bufSince) > 10*time.Second
		cooled := time.Since(p.lastRecon) > 15*time.Second
		url, title := p.lastURL, p.lastTitle
		if stalled && cooled {
			p.lastRecon = time.Now()
		}
		p.mu.Unlock()
		if !running {
			return
		}
		if stalled && cooled {
			p.sendLocked([]interface{}{"loadfile", url, "replace"})
			p.sendLocked([]interface{}{"set_property", "force-media-title", title})
		}
	}
}

func (p *Player) sendLocked(cmd interface{}) {
	if p.sock == "" {
		return
	}
	c, err := net.Dial("unix", p.sock)
	if err != nil {
		return
	}
	defer c.Close()
	b, _ := json.Marshal(map[string]interface{}{"command": cmd})
	if _, err := c.Write(append(b, '\n')); err != nil {
		return
	}
	// Drain mpv's one-line reply before closing. We open/write/close per
	// command deliberately (a persistent write connection can hang mpv on
	// repeated loadfile — mpv#3422/#5683), but closing before reading the
	// reply makes mpv log "Write error (Broken pipe)" as it fails to deliver
	// the response. A short read gives it somewhere to send that reply.
	_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _ = bufio.NewReader(c).ReadBytes('\n')
}

// SetTitle updates the on-screen media title (used to show the current EPG
// programme). It also remembers the title so a reconnect keeps it.
func (p *Player) SetTitle(title string) {
	p.mu.Lock()
	p.lastTitle = title
	sock := p.sock
	running := p.running
	p.mu.Unlock()
	if sock == "" || !running {
		return
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", sock); err == nil {
			b, _ := json.Marshal(map[string]interface{}{
				"command": []interface{}{"set_property", "force-media-title", title}})
			if _, err := c.Write(append(b, '\n')); err == nil {
				// drain the reply so mpv doesn't broken-pipe (see sendLocked)
				_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				_, _ = bufio.NewReader(c).ReadBytes('\n')
			}
			_ = c.Close()
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	p.cmd = nil
	p.running = false
}
