# md-review

A terminal reviewer for markdown documents: styled prose, real mermaid diagrams
rendered as images, and comments anchored to what you were reading.

```
md-review design.md
```

Comments are stored beside the document as `design.review.json` and saved after
every change. Nothing is written until you actually leave a comment.

## Diagrams

Mermaid blocks render as real images, not ASCII approximations, using the Kitty
graphics protocol. Ghostty, kitty, and WezTerm are detected automatically.

Diagrams are rasterized by [mermaid-cli], which needs a one-time install:

```
md-review --install-mermaid    # into ~/Library/Caches/md-review/tools
md-review --selftest           # draws a test diagram so you can confirm it works
```

Rasterizing is slow (a headless browser boots), so every diagram is cached on
disk under a hash of its source. Renders happen in the background: the document
is readable immediately and diagrams appear as they finish.

Diagram size is corrected for display density, so labels come out roughly the
same visual size as terminal text on both standard and HiDPI screens. Terminals
report cell size in physical pixels while mermaid rasterizes in CSS pixels, and
without that correction every diagram renders at half scale on a 2x display. The
ratio is inferred from cell height; `-diagram-scale` overrides the result if you
want diagrams larger or smaller.

Without mermaid-cli, or in a terminal without image support, diagram blocks show
their own source instead — the block stays reviewable either way.

[mermaid-cli]: https://github.com/mermaid-js/mermaid-cli

## Keys

| Key | Action |
|-----|--------|
| `j` / `k` | previous / next block |
| `ctrl+d` / `ctrl+u` | half page |
| `g` / `G` | first / last block |
| `T` | table of contents |
| `/`, then `n` / `N` | search, next, previous |
| `c` | comment on the current block |
| `⌥↵` or `ctrl+s` | save the comment being composed |
| `e` | edit the selected comment |
| `x` | delete the selected comment (asks first) |
| `r` | toggle resolved |
| `tab` | cycle comments within a block |
| `]` / `[` | next / previous annotated block |
| `t` | show or hide resolved comments |
| `R` | reload the document from disk |
| `q` | quit |

## Saving a comment with Cmd+Return

The composer saves on `alt+return` or `ctrl+s`. Plain `return` inserts a
newline, so comments can be multi-line.

`cmd+return` needs one line of terminal config, for two reasons that are worth
knowing before you fight it:

1. macOS never delivers the Cmd (Super) modifier to a TTY. The only wire format
   that can express it is the Kitty keyboard protocol, and Bubble Tea v1 does not
   parse CSI-u sequences at all — enabling the protocol would break every other
   key rather than add one.
2. Ghostty binds `super+enter` to `toggle_fullscreen` by default, so the chord is
   consumed before the TTY ever sees it.

So the terminal has to translate the chord into something deliverable. In
`~/.config/ghostty/config`:

```
keybind = super+enter=text:\x1b\x0d
```

That sends `ESC CR`, which is exactly what `alt+return` sends, and md-review
already saves on it. Note this **replaces** ghostty's `super+enter` fullscreen
toggle globally, and applies in every terminal app — that trade is yours to make.

To confirm what actually arrives:

```
md-review --keys
```

Press the chord. `1b 0d` means it worked; nothing at all means the terminal is
still swallowing it.

## How comments stay attached

Comments anchor to a **block** — a paragraph, heading, list, table, or code
fence — not to a line number. The anchor is a hash of the block's
whitespace-normalized text, so:

- Editing prose *above* a comment moves the comment with its block.
- Reflowing or rewrapping the commented paragraph keeps the comment attached.
- Rewriting the commented text **orphans** the comment. Orphans are shown with a
  `⚠` marker and keep an excerpt of what they were about, so they are visible
  and recoverable rather than silently re-pointed at unrelated prose.

Press `R` after editing the file elsewhere to re-anchor without restarting.

## Flags

```
-style dark|light|auto      markdown styling (default auto)
-mermaid-theme NAME         dark, default, neutral, forest
-mmdc PATH                  explicit mermaid-cli location
-no-graphics                show diagram source instead of images
-max-diagram-rows N         tallest a diagram may render (default 30)
-diagram-scale F            multiply diagram size (default 1.0)
-read-only                  browse without allowing comment changes
-selftest                   verify terminal graphics support
-keys                       print raw bytes of each keypress
-paths                      print binary and cache locations
```

Environment overrides: `MD_REVIEW_MMDC` (binary path), `MD_REVIEW_CELL=WxH`
(cell pixel size, if your terminal misreports it), `MD_REVIEW_GRAPHICS=0|1`
(force image support off or on).

## Sidecar format

```json
{
  "version": 1,
  "document": "design.md",
  "notes": [
    {
      "id": "9f2c1a8b4d6e",
      "block_id": "3a7f21c9d4e8b012",
      "start_line": 42,
      "end_line": 45,
      "quote": "We retry failed deliveries three times with exponential backoff…",
      "body": "Too short for a broker failover, which takes 10-15s.",
      "author": "Justin Abrahms",
      "created_at": "2026-08-25T19:35:00Z"
    }
  ]
}
```

Stable ordering and indented JSON, so it diffs cleanly in git and other tools
can read it.

## Notes on the implementation

Diagrams are drawn with Kitty **Unicode placeholders** rather than direct cursor
placement. Each image occupies real text cells, so it scrolls, clips, and diffs
like any other content instead of floating above the frame and smearing when the
view moves. Two consequences shape the code:

- Every row is measured and clipped to the viewport, and tabs are expanded before
  glamour lays out a block. A line that measures narrower than it renders would
  wrap, push the frame past the window height, and tear every image below it.
- Frames and graphics commands share one locked writer, so no escape sequence can
  be split by another writer mid-flight.
