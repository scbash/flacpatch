package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/mewkiz/flac"
	"github.com/schollz/progressbar/v3"
)

// TODO: command line switch between default time (this) and CUE sheet time (CDTime below)
func fmtTime(t time.Duration) string {
	minutes := t.Truncate(time.Minute)
	seconds := t - minutes

	return fmt.Sprintf("%02.0f:%02.3f", minutes.Minutes(), seconds.Seconds())
}

// Format duration like CD time CUE sheet (including frames)
// "The position is specified in mm:ss:ff (minute-second-frame) format. There are 75 such frames per second of audio."
// https://en.wikipedia.org/wiki/Compact_Disc_Digital_Audio#Frames_and_timecode_frames
// https://en.wikipedia.org/wiki/Cue_sheet_(computing)
// https://forum.videohelp.com/threads/394177-What-s-the-sector-format-on-audio-CDs
func fmtCDTime(t time.Duration) string {
	minutes := t.Truncate(time.Minute)
	only_seconds := t - minutes
	seconds := only_seconds.Truncate(time.Second)
	only_frames := only_seconds - seconds
	frames := only_frames.Round(time.Duration(time.Second / 75))

	return fmt.Sprintf("%02.0f:%02.0f:%02.0f", minutes.Minutes(), seconds.Seconds(), float64(frames.Nanoseconds())/1e7)
}

func main() {
	// TODO: real arg parsing...
	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s path/to/file.flac\n", os.Args[0])
	}

	filename := os.Args[1]
	f, err := os.Open(filename)
	if err != nil {
		log.Fatalf("Unable to open file %s: %s\n", filename, err)
	}

	stream, err := flac.NewSeek(f)
	if err != nil {
		log.Fatalln("Error creating FLAC stream: ", err)
	}

	// Calculate first two bytes of FLAC frame header (easier than reading 14 bits...)
	// 14 bits: sync-code (11111111111110)
	// 1 bit: reserved.
	// 1 bit: HasFixedBlockSize.
	searchBytes := []byte{0xFF, 0xF7}
	if stream.Info.BlockSizeMin == stream.Info.BlockSizeMax {
		searchBytes[1] = 0xF8
	}

	// TODO: lastGoodSample is more useful (and applicable to both blocking styles), but will be basically unreadable numbers
	var lastGood uint64
	lastGood = 0
	bar := progressbar.Default(int64(stream.Info.NSamples), "reading")
	for {
		frame, err := stream.ParseNext()
		if err == io.EOF {
			progressbar.Bprintf(bar, "Reached EOF, ending...")
			bar.Finish()
			break
		}
		if err != nil {
			progressbar.Bprintf(bar, "Found bad frame (lastGood = %d), searching for next good one...\n", lastGood)
			byte_offset := 0
			for {
				buf := make([]byte, 2)
				_, err2 := f.Read(buf)
				byte_offset += 2
				if err2 != nil {
					log.Fatal(err2)
				}
				if buf[0] == searchBytes[0] && buf[1] == searchBytes[1] {
					f.Seek(-2, io.SeekCurrent)
					inner_frame, err2 := stream.ParseNext()
					if err2 == nil {
						progressbar.Bprintf(bar, "Found good header! (offset: %d) %d\n", byte_offset, inner_frame.Num)

						startSample := lastGood * uint64(stream.Info.BlockSizeMin)
						endSample := (inner_frame.Num - 1) * uint64(stream.Info.BlockSizeMin)
						start := time.Duration(float64(startSample) / float64(stream.Info.SampleRate) * float64(time.Second))
						end := time.Duration(float64(endSample) / float64(stream.Info.SampleRate) * float64(time.Second))

						progressbar.Bprintf(bar, "bad region: %s-%s (%s)\n", fmtCDTime(start), fmtCDTime(end), fmtTime(end-start))
						frame = inner_frame
						break
					}
				}
			}
		}
		lastGood = frame.Num
		bar.Set64(int64(frame.SampleNumber()))
	}
}
