"""Regenerate synthetic APE fixtures: python3 generate.py /path/to/mac."""

import hashlib
from pathlib import Path
import subprocess
import sys
import tempfile
import wave


root = Path(__file__).resolve().parent
for bits, rate in ((16, 44100), (24, 96000)):
    width = bits // 8
    pcm = bytearray()
    scale = 1 << (bits - 16)
    for sample in range(3 * rate):
        for period, slope, phase in ((2048, 13, 17), (4096, 7, 43)):
            value = (sample % period - period // 2) * (slope * scale + scale - 1) + phase
            pcm.extend(value.to_bytes(width, "little", signed=True))
    name = f"stereo-{bits}"
    sheet = f'''PERFORMER "Synthetic Artist"
TITLE "Synthetic Album"
FILE "{name}.ape" WAVE
  TRACK 01 AUDIO
    TITLE "First"
    INDEX 01 00:00:10
  TRACK 02 AUDIO
    TITLE "Second"
    INDEX 00 00:01:05
    INDEX 01 00:01:17
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
