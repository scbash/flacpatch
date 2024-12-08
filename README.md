flacpatch
=========
This is a proof-of-concept implementation for repairing damaged/corrupted FLAC files.
The [FLAC file format](https://xiph.org/flac/format.html) is fairly resiliant, so when
an unreliable file transfer left "empty" blocks in the middle of my FLAC files, I
decided to see if it was possible to recover the audio _after_ the missing blocks, and
yes it is -- and this repo implements one method of doing it (I won't claim this is
the _best_ method, but it does work).

## Approach
FLAC divides audio into _blocks_ and _frames_.  Blocks represent uncompressed audio,
while frames are the result of compressing the audio.  Blocks can be either fixed or
variable size, but all my corrupted files were fixed, so I haven't implemented/tested
variable block size.  Frames will vary in size based on compression efficiency, but
frame headers include enough information to know which _block_ in the uncompressed
audio they correspond to, so it's always possible to know (and reconstruct) the audio
time after a gap.  When this proof-of-concept encounters a bad FLAC frame, it simply
scans forward in the file until it finds a valid frame (which can only be confirmed by
fully parsing the header and checking the CRC, see [notes here](https://xiph.org/flac/format.html#format_overview)).

With all the bad regions noted, this proof-of-concept then loads the CUE sheet and
exports tracks that do not intersect with any bad regions.  Technically we could
output all good frames, but I personally don't want to listen to part of a song,
so I aligned with track boundaries.

## Implementation notes
- This repo and `diagnose.go` are not particularly well named; I originally had a
  slightly different approach in mind, but as PoCs go, things evolved and here we
  are.  It's not so much _patching_ FLAC as "grabbing the good bits", but for a
  quick hack it's fine.
- My CUE sheets had byte-order marks and other features that broke [lmvgo/cue](https://github.com/lmvgo/cue),
  so I had to fork it and make some modifications.  My fork is recorded as a
  `replace` in go.mod.
- I chose to shell out to the reference FLAC executable rather than attempting to
  encode with [mewkiz/flac](https://github.com/mewkiz/flac) because at the time of
  this writing, the encoding support in the Go library was pretty new and still
  evolving.  The reading code works great, and plays well with this crazy idea I
  came up with, so no complaints!
- Overall this code is kind of cobbled together, so things might be done in a bit
  of an odd way, don't take this as an example of amazing Go code...
