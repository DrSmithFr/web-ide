"""Beats of the track of the promo (librosa, run with `uv run --with librosa`).

Writes promo/out/beats.json: the beat grid (tempo and phase from the onset envelope), the
kicks (onsets of the low band, with their strength), the onsets of the whole signal, and the
sections where the bass plays or stops (intro, verse, build, break, drop of the edit).
"""
import glob
import json
import os
import sys

import librosa
import numpy as np

HERE = os.path.dirname(os.path.abspath(__file__))
track = sys.argv[1] if len(sys.argv) > 1 else sorted(glob.glob(os.path.join(HERE, '*.mp3')))[0]
y, sr = librosa.load(track, sr=22050, mono=True)
duration = len(y) / sr
end = float(np.argwhere(np.abs(y) > 0.02)[-1][0] / sr)  # the fade out ends there

oenv = librosa.onset.onset_strength(y=y, sr=sr)
times = librosa.times_like(oenv, sr=sr)
# The grid: the period and the phase that put the kicks of the whole track (with their
# strength) closest to eighths of a beat, searched around the tempo of the beat tracker.
tracked_tempo = float(np.atleast_1d(librosa.beat.beat_track(y=y, sr=sr, start_bpm=100)[0])[0])

S = np.abs(librosa.stft(y))
freqs = librosa.fft_frequencies(sr=sr)
st = librosa.times_like(S, sr=sr)
low = S[freqs < 150].sum(axis=0)
low_flux = np.maximum(0, np.diff(low, prepend=low[0]))
kick_times = librosa.onset.onset_detect(onset_envelope=low_flux, sr=sr, units='time')
kick_strength = np.interp(kick_times, st, low_flux / low_flux.max())
kicks = [[round(float(t), 3), round(float(s), 3)] for t, s in zip(kick_times, kick_strength) if s > 0.08]

best = (0.0, 0.0, 0.0)
kt = np.array([k[0] for k in kicks]); ks = np.array([k[1] for k in kicks])
for p in np.arange(60 / (tracked_tempo * 1.04), 60 / (tracked_tempo * 0.96), 0.0002):
    eighth = p / 2
    for phase in np.arange(0, eighth, 0.002):
        d = (kt - phase) / eighth
        dist = (d - np.round(d)) * eighth
        score = float(np.sum(ks * np.exp(-(dist ** 2) / (2 * 0.015 ** 2))))
        if score > best[0]:
            best = (score, p, phase)
period = best[1]
# The beat (not the off-beat eighth) is the one with the strongest kicks.
on_beat = lambda ph: float(np.sum(ks * np.exp(-((((kt - ph) / period) - np.round((kt - ph) / period)) * period) ** 2 / (2 * 0.015 ** 2))))
t0 = best[2] if on_beat(best[2]) >= on_beat(best[2] + period / 2) else best[2] + period / 2
t0 = t0 % period
tempo = 60.0 / period
beats = [round(float(t0 + i * period), 3) for i in range(int((end - t0) / period) + 1)]

on_times = librosa.onset.onset_detect(onset_envelope=oenv, sr=sr, units='time')
on_strength = np.interp(on_times, times, oenv / oenv.max())
onsets = [[round(float(t), 3), round(float(s), 3)] for t, s in zip(on_times, on_strength)]

# Bass on or off, smoothed over half a second: the sections of the edit.
low_level = np.convolve(low / low.max(), np.ones(22) / 22, mode='same')
bass = low_level > 0.12
sections, start = [], 0.0
for i in range(1, len(bass)):
    if bass[i] != bass[i - 1] and st[i] - start > 0.8:
        sections.append({'start': round(start, 3), 'end': round(float(st[i]), 3), 'bass': bool(bass[i - 1])})
        start = float(st[i])
sections.append({'start': round(start, 3), 'end': round(end, 3), 'bass': bool(bass[-1])})
merged = []
for sec in sections:
    if sec['end'] <= sec['start']:
        continue
    if merged and merged[-1]['bass'] == sec['bass']:
        merged[-1]['end'] = sec['end']
    else:
        merged.append(sec)
sections = merged

out = {'track': os.path.basename(track), 'duration': round(duration, 3), 'end': round(end, 3), 'tempo': round(tempo, 2),
       'period': round(period, 4), 'beats': beats, 'kicks': kicks, 'onsets': onsets, 'sections': sections}
os.makedirs(os.path.join(HERE, 'out'), exist_ok=True)
with open(os.path.join(HERE, 'out', 'beats.json'), 'w') as f:
    json.dump(out, f, indent=1)
print(f"tempo {out['tempo']} bpm, {len(beats)} beats, {len(kicks)} kicks, end {out['end']} s")
for s in sections:
    print(f"  {s['start']:6.2f} → {s['end']:6.2f}  {'bass' if s['bass'] else 'no bass'}")
