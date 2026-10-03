"""Regenerate synthetic APE fixtures: python3 generate.py /path/to/mac."""

import hashlib
from pathlib import Path
import subprocess
import sys
import tempfile
import wave


root = Path(__file__).resolve().parent
for bits, rate, seconds, name in ((16, 44100, 3, "stereo-16"), (24, 96000, 3, "stereo-24"), (16, 44100, 12, "late-16")):
    if len(sys.argv) > 2 and sys.argv[2] == "--late-only" and seconds != 12:
        continue
    width = bits // 8
    pcm = bytearray()
    scale = 1 << (bits - 16)
    for sample in range(seconds * rate):
        for period, slope, phase in ((2048, 13, 17), (4096, 7, 43)):
            value = (sample % period - period // 2) * (slope * scale + scale - 1) + phase
            pcm.extend(value.to_bytes(width, "little", signed=True))
    second = 10 if seconds == 12 else 1
    sheet = f'''PERFORMER "Synthetic Artist"
TITLE "Synthetic Album"
FILE "{name}.ape" WAVE
  TRACK 01 AUDIO
    TITLE "First"
    INDEX 01 00:00:10
  TRACK 02 AUDIO
    TITLE "Second"
    INDEX 00 00:{second:02d}:05
    INDEX 01 00:{second:02d}:17
'''
    destination = root / f"{name}.ape"
    destination.unlink(missing_ok=True)
    with tempfile.TemporaryDirectory() as directory:
        source = Path(directory) / "source.wav"
        with wave.open(str(source), "wb") as output:
            output.setparams((2, width, rate, 0, "NONE", "not compressed"))
            output.writeframes(pcm)
        subprocess.run([sys.argv[1], str(source), str(destination), "-c2000", "-threads=1"], check=True)
    # Also exercise the real APEv2 text CUESHEET extraction path.
    subprocess.run([sys.argv[1], str(destination), "-t", f"CUESHEET={sheet}"], check=True)
    (root / f"{name}.cue").write_text(sheet)
    print(f"{name}: decoded PCM SHA-256 {hashlib.sha256(pcm).hexdigest()}")
