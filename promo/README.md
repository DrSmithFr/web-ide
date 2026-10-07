# Promo video

A 22 s vertical video (1080×1920, 30 fps) of Web IDE for TikTok and Reels: black and white,
very fast cuts and inversions synced to the beats of a track, the real UI and giant type.

```
make promo                          # the track: the first promo/*.mp3
make promo PROMO_TRACK=/path/x.mp3  # or another one
```

1. `capture.cjs` runs on a fresh pod (through `e2e/run.sh`) with a scripted model and takes
   the shots of the UI in `out/shots/`: editor, kanban, the Orchestrator, sub-agents, a
   question, an app preview, the home page, the phone layout (pixel ratio 3).
2. `analyze.py` (librosa, through `uv run`) writes `out/beats.json`: the beat grid (period and
   phase fitted on the kicks of the whole track), the kicks, the onsets, and the sections
   where the bass plays or stops.
3. `render.cjs` opens `stage.html`, whose `timeline.js` draws any time `t` the same way every
   time, takes each frame and pipes it to ffmpeg:
   - `out/webide-promo.mp4`: muted, for TikTok (add the sound from its library there);
   - `out/webide-promo-preview.mp4`: with the track, to check the sync. Private: the track
     is not licensed for anything else.

`node promo/render.cjs --sheet [--from s --to s --step n]` writes a contact sheet of frames
instead of the video.

The edit follows the sections of the track: the intro (a word per beat), the verse without
bass (one shot of the UI per beat with its word), the build (the sub-agents multiply, FASTER on
every eighth), the break (the Orchestrator types its question), the drop (a cut per eighth, an
inversion per beat) and the outro on black, which loops.

## Site video

A 45 s demo for the site and the README (1920×1080, 30 fps, silent): a wide shot, then the
camera follows one story through the real UI. An idea is typed in the chat, the assistant asks a
question, the ticket lands on the board, its plan fills the ticket. "LET'S GOOOO" starts a
developer agent in its worktree. The roadmap opens the ticket, then the preview of the app.

```
make promo-site
```

1. `site/capture.cjs` runs on a fresh pod (through `e2e/run.sh`) with a scripted model that
   streams its text and holds its steps until the script releases them. It records **scenes**
   with the screencast of the browser (pixel ratio 2): a frame at each change of the screen, with
   its time. It also keeps the clicks, for a drawn cursor, and the rects of the parts of the UI,
   all in `out/site/manifest.json`.
2. `render.cjs --site` opens `site/stage.html`, whose `site/timeline.js` plays each shot. A shot
   is a range of a scene at its own speed, with camera keyframes (centre, zoom), the cursor and a
   caption. Then come the "Later…" card and the end card. Output: `out/webide-site.mp4`.

Fonts: Anton and JetBrains Mono, under the SIL Open Font License (`fonts/`).
