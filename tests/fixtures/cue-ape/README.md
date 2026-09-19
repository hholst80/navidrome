# Synthetic APE fixtures

These three-second stereo signals are generated from integer arithmetic by `generate.py`; they contain no recorded music. The fixtures and generator use the repository's license. Both images have an APEv2 text `CUESHEET` tag and a matching external sheet. Their nonzero first INDEX 01 and second-track INDEX 00 exercise preservation of recorded pregaps.

The 16-bit fixture uses 44,100 Hz and the 24-bit fixture uses 96,000 Hz. Tests require only FFmpeg's APE decoder and lossless output encoders; the Monkey's Audio encoder is needed only to regenerate fixtures.

Generation used the [official Monkey's Audio 13.26 SDK](https://monkeysaudio.com/files/MAC_1326_SDK.zip), archive SHA-256 `3fdb516db15cc754eb2db1d255e405a8142fbb115eccdf51b0fa07b84305b6ac`. Build it with `cmake -S sdk -B build -DCMAKE_BUILD_TYPE=Release` and `cmake --build build`, then run `python3 tests/fixtures/cue-ape/generate.py /absolute/path/to/build/mac`. The generator prints the decoded PCM checksums. No SDK code or binaries are distributed here.

Expected SHA-256 checksums of interleaved signed little-endian PCM at the source bit depth:

- `stereo-16.ape`: `23d363f881949499e25568fda5fd02d54c73a42b7cddb4ec45eaae874b166398`
- `stereo-24.ape`: `16dd91941379324cfcf105f751d27d6e1a52f4038d1586c0348a052c69ca5235`
