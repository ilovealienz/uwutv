# uwutv

A terminal client for an Xtream Codes IPTV subscription. Browse live channels,
movies and series in a two-pane TUI; playback happens in mpv.

You bring your own subscription — this is only the front-end.

## What it does

- **Live TV** with EPG (now / next / then) and channels grouped by quality, so
  `BBC 1 [HD 4K HEVC HD]` is one row that expands to a picker
- **Movies** and **Series** (seasons → episodes) with plot, cast, rating
- One mpv instance, reused: switching channels replaces the stream in place, so
  the window never moves and you never exceed a one-stream provider limit
- mpv's title follows the current programme and updates when it changes
- Favourites and category hiding in every mode
- Watch tracking: `✓` watched, `◐` partly watched, next-episode playback
- Auto-reconnect if a live stream stalls

## Requirements

Go 1.22+ to build, `mpv` on `PATH` to play. Linux only — mpv IPC uses a Unix
socket.

## Build

```sh
./build.sh          # build to ./uwutv
./build.sh run      # build, then run
./build.sh clean
```

`make`, `make run`, `make clean` do the same. By hand:

```sh
go mod tidy
CGO_ENABLED=0 go build -o uwutv .
```

`CGO_ENABLED=0` avoids needing a C compiler and produces a fully static binary,
so the same file runs on any Linux distribution regardless of libc.

Prebuilt binaries for amd64, arm64 and armv7 are attached to each release.
Download, extract, and put `uwutv` somewhere on your `PATH`.

## First run

Running `uwutv` with no config writes `~/.config/uwutv/config.toml` and exits.
Fill in `host`, `username` and `password`, then run it again.

```toml
host     = "http://example.com:8080"
username = "yourusername"
password = "yourpassword"
```

## Keys

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | move |
| `enter` | open category / play / pick quality |
| `esc` | back — returns to where you were; quits at the top level |
| `tab` | mode menu: live / movies / series |
| `/` | search (`esc` clears, `enter` keeps the filter) |
| `ctrl+f` | favourite the channel, movie, series or category |
| `w` | toggle watched — on a season or series, marks every episode |
| `n` | play the next episode |
| `h` | hide the highlighted category |
| `H` | show hidden categories, so they can be unhidden |
| `ctrl+r` | re-fetch the current mode's catalogue |
| `q` / `ctrl+c` | quit |

## Config

`~/.config/uwutv/config.toml`. Everything except the credentials is optional.

| Key | Meaning |
|---|---|
| `host` `username` `password` | Xtream account |
| `ext` | live stream container: `ts` (default) or `m3u8` |
| `user_agent` | sent on every request, including the stream. Defaults to `TiviMate/4.7.0`, since some providers reject unknown clients |
| `priority` `movie_priority` `series_priority` | categories pinned to the top, per mode, in the order listed |
| `live_show` `movie_show` `series_show` | whitelist — when non-empty, only matching categories appear |
| `live_hide` `movie_hide` `series_hide` | blacklist — always wins over `*_show` |
| `autoplay_next` | start the next episode when one ends (default `true`) |
| `mpv_args` | extra mpv flags, appended so they override the built-ins. Set `["--msg-level=ffmpeg/video=no"]` to silence live H.264 SPS log noise |
| `epg_offset` | guide shift in hours, e.g. `-1.0` if the guide reads an hour late, `2.0` if two hours early, `-0.5` for half an hour. Only needed if the guide is wrong |

Priority matches either the code before a `|` or a leading prefix, so `"UK"`
catches `UK| GENERAL` and `"EN -"` catches `EN - WWE`. The character after a
prefix must be non-alphanumeric, so `"UK"` won't match `UKRAINE`.

Show/hide take globs (`*`, `?`), case-insensitive, against the whole category
name — e.g. `series_show = ["ENGLISH*", "NETFLIX*"]` hides everything else.

### Environment variables

Five variables override the matching config value at startup. The config file
is still read; anything set in the environment simply wins over it.

| Variable | Overrides |
|---|---|
| `XTREAM_HOST` | `host` |
| `XTREAM_USER` | `username` |
| `XTREAM_PASS` | `password` |
| `XTREAM_EXT` | `ext` |
| `XTREAM_UA` | `user_agent` |

Set them inline, before the command, for a single run:

```sh
XTREAM_EXT=m3u8 uwutv                    # one variable
XTREAM_HOST=http://example.com:8080 XTREAM_USER=me XTREAM_PASS=secret uwutv
```

Or export them first, so they apply to every `uwutv` in that shell:

```sh
export XTREAM_HOST=http://example.com:8080
export XTREAM_USER=me
export XTREAM_PASS=secret
uwutv
```

The main reason to bother is keeping credentials out of the config file: leave
`username` and `password` empty in the TOML and pull them from a password
manager at launch. As a shell function in `.bashrc` / `.zshrc`:

```sh
uwutv() {
  XTREAM_USER="$(pass show iptv/user)" \
  XTREAM_PASS="$(pass show iptv/pass)" \
  command uwutv "$@"
}
```

They're also handy for testing a change without editing anything:

```sh
XTREAM_EXT=m3u8 uwutv        # try HLS instead of .ts
XTREAM_UA=uwutv/0.1 uwutv    # see whether the provider cares about User-Agent
```

Two more control paths rather than values:

`UWUTV_CONFIG` points at a different config file, which is how you run more than
one account — each gets its own file, and they share nothing but the binary:

```sh
UWUTV_CONFIG=~/.config/uwutv/second.toml uwutv
```

`XDG_CONFIG_HOME`, `XDG_DATA_HOME` and `XDG_CACHE_HOME` relocate the three
directories in the table above:

```sh
XDG_DATA_HOME=/mnt/backup/share uwutv    # keep favourites/history elsewhere
```

Note that separate accounts sharing the default paths will also share
favourites and watch history, since those live in the data directory rather
than beside the config. Set `XDG_DATA_HOME` too if you want them kept apart.

## Where files live

| Path | Contents |
|---|---|
| `~/.config/uwutv/` | `config.toml` |
| `~/.local/share/uwutv/` | favourites, hidden categories, watch history — **back this up** |
| `~/.cache/uwutv/` | catalogues and item metadata — safe to delete, re-fetches |

State from older versions is moved out of the cache directory automatically.

Metadata caches are append-only and capped at 8 MiB each, evicting the
least-recently-viewed entries. Catalogues refresh when they're a day old or on
`ctrl+r`. Movies and series aren't fetched at all until you first open them.

## Source layout

| File | Contents |
|---|---|
| `main.go` | TUI model, navigation, layout, status bar |
| `xtream.go` | config, live API, caching, quality grouping |
| `vod.go` | movies/series types, API, stream URLs |
| `vod_ui.go` | mode switching, VOD lists, detail pane, watch progress |
| `player.go` | mpv process, IPC observer, stall watchdog |
| `favourites.go` | favourites and hidden categories |
| `watch.go` | watch history, metadata cache, state migration |
