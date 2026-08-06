# Brief: a custom icon set for Skywire Mobile

Hand this to whoever draws the icons. Everything below is the constraint the
app already enforces — the placeholder set that ships today was drawn to it,
so a set that follows it drops in without a single layout change.

---

## What we need

A single-weight line icon set, 49 glyphs, for the Skywire phone app: the
SkyChat surface (which is HTML rendered inside the app) and the native
screens around it. They replace a set we drew ourselves as placeholders,
which in turn replaced platform emoji.

## Why it matters here

Two of these live somewhere unusual, and it shapes the whole set:

- **They are inlined as SVG paths into a single HTML file** and coloured with
  `currentColor`. So each icon must be *one path group, no fills, no
  hard-coded colours, no gradients, no masks, no clip paths, no `<defs>`*.
  Anything that cannot be expressed as stroked paths on a 24-unit grid we
  cannot use.
- **They must read at 11px.** The delivery tick under a chat bubble is drawn
  at 11–14px. That is the real test of this set, not the 20px menu size.

## The grid and the line

| | |
|---|---|
| Canvas | 24 × 24 |
| Live area | 20 × 20 (2 units clear on every side) |
| Stroke | 2 units, uniform, no tapering |
| Caps and joins | round |
| Corner radius | 2 units on square-ish forms |
| Alignment | snap to whole units; half-units only where a form must centre |
| Colour | none — strokes inherit `currentColor` |
| Fills | none, except the four noted below |

Four glyphs are solid rather than stroked, because a 2-unit outline at 13px
leaves a hole where the shape should be: `play`, `dots`, `dotsH`. Draw these
as filled silhouettes with no stroke.

Optical sizing matters more than mathematical: a circle and a square that
both measure 20 units do not look the same size. Balance by eye.

## The palette they sit in

Icons are monochrome and inherit their colour from the text beside them, so
there is nothing to specify per icon — but they are seen against these, and
must hold up on both:

```
Dark    background #000000   surface #101216   text #FFFFFF / #9EA5AD / #6B7279
Light   background #FFFFFF   surface #F6F7F9   text #000000 / #565C64 / #8A9099
Accent  #0072FF  (both themes)
Status  ok #3FD07E/#0B7A3E   warn #F0A93B/#8A5600   bad #FF6B5E/#C42B1C
```

The typeface is Skycoin (geometric, generous counters, weights 400 and 700
only). The icons should feel drawn by the same hand: geometric construction,
circles that are actually circular, few incidental details.

## The 49 glyphs

Grouped by where they appear, because a group should feel internally
consistent even more than the set does overall.

**Identity and people** — `user` (one person), `users` (two or three people,
a group), `book` (address book, a bound book), `qr` (QR code: three finder
squares plus a scatter of modules), `edit` (pencil), `bookmark` (Saved
Messages — a ribbon bookmark).

**Conversations** — `chat` (a speech bubble; used at 56px on the empty
state, so it carries more weight than the rest), `hash` (#, a group),
`megaphone` (a channel — broadcast, one-to-many), `pin`, `bell`, `bellOff`,
`link` (paired), `unlink` (unpaired), `leave` (leave a group: a door with an
arrow going out).

**Composing and messages** — `send` (a paper plane), `paperclip`
(attachment), `mic`, `micOff`, `video` (a camcorder), `camera` (a stills
camera — distinct from `video` at 16px, which is the hard part), `image` (a
picture frame), `reply` (arrow curving back left), `forward` (arrow curving
on right), `trash`, `copy`, `download`.

**Delivery state** — `circle` (sending), `check` (sent), `checkDouble`
(received/read), `alert` (failed — a warning triangle). These four are the
11–14px set. They must be distinguishable from each other at a glance, at
that size, in peripheral vision, because that is how they are actually read.

**Calls** — `phone` (handset), `hangup` (handset with a slash), `callOut`
(arrow leaving, up-right), `callIn` (arrow arriving, down-left), `volume`,
`volumeOff`, `chart` (a spectrogram/level display — vertical bars).

**Chrome and navigation** — `left`, `right`, `up` (chevrons), `plus`,
`close`, `dots` (vertical ⋮ overflow), `dotsH` (horizontal ⋯ overflow),
`gear` (settings), `lock`, `play`, `pause`.

## Pairs that must not collide

These are the ones a generic set usually gets wrong for us:

- `video` vs `camera` — moving vs still, and both appear in the same
  composer.
- `check` vs `checkDouble` — at 12px the difference has to survive.
- `bell` vs `bellOff`, `mic` vs `micOff`, `volume` vs `volumeOff`, `link` vs
  `unlink`, `phone` vs `hangup` — each "off" state is the same glyph plus a
  slash. Use one consistent slash: same angle, same length, same relationship
  to the base glyph, across all five. Today they vary slightly and it shows.
- `users` vs `megaphone` — "a group" vs "a channel" is a distinction our
  users have to make constantly.

## Deliverables

- One SVG per glyph, named exactly as above (`callOut.svg`, `checkDouble.svg`
  — camelCase, matching the keys the code uses).
- 24 × 24 viewBox, paths only, strokes not expanded to outlines, no
  `<defs>`, no `style` attributes, no `fill` or `stroke` attributes on the
  paths themselves (the app sets those).
- A single contact sheet PNG at 16px and at 48px, both themes, for review.

## What we will do with them

Drop the path data into one table in the app. There is a single `icon(name,
size)` helper; nothing else has to change. If a glyph is missing the app
renders nothing rather than breaking, so the set can land in pieces.

---

### The short version, if you want to paste a prompt

> Draw a 49-glyph monochrome line icon set for a peer-to-peer messaging and
> crypto-wallet phone app. 24×24 grid, 20×20 live area, uniform 2-unit
> stroke, round caps and joins, 2-unit corner radii, geometric construction
> to match a geometric sans (Skycoin). Paths only — no fills, colours,
> gradients or clip paths; the app colours them with currentColor and renders
> them from 11px to 56px, so legibility at 12px is the binding constraint.
> `play`, `dots` and `dotsH` are solid silhouettes instead. Glyphs: [list
> above]. The five on/off pairs (bell, mic, volume, link, phone) must share
> one consistent slash treatment, and video vs camera, check vs
> check-double, and users vs megaphone must stay distinguishable at 16px.
> Deliver one SVG per glyph, camelCase filenames, plus a contact sheet at
> 16px and 48px on both a #000000 and a #FFFFFF background.
