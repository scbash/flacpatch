package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	cuesheetgo "github.com/lmvgo/cue"
	"github.com/mewkiz/flac"
	"github.com/schollz/progressbar/v3"
)

// TODO: command line switch between default time (this) and CUE sheet time (CDTime below)
func fmtTime(t time.Duration) string {
	minutes := t.Truncate(time.Minute)
	seconds := t - minutes

	return fmt.Sprintf("%02.0f:%06.3f", minutes.Minutes(), seconds.Seconds())
}

// Format duration like CD time CUE sheet (including frames)
// "The position is specified in mm:ss:ff (minute-second-frame) format. There are 75 such frames per second of audio."
// https://en.wikipedia.org/wiki/Compact_Disc_Digital_Audio#Frames_and_timecode_frames
// https://en.wikipedia.org/wiki/Cue_sheet_(computing)
// https://forum.videohelp.com/threads/394177-What-s-the-sector-format-on-audio-CDs
// func fmtCDTime(t time.Duration) string {
// 	minutes := t.Truncate(time.Minute)
// 	only_seconds := t - minutes
// 	seconds := only_seconds.Truncate(time.Second)
// 	only_frames := only_seconds - seconds
// 	frames := only_frames.Round(time.Duration(time.Second / 75))
//
// 	return fmt.Sprintf("%02.0f:%02.0f:%02.0f", minutes.Minutes(), seconds.Seconds(), float64(frames.Nanoseconds())/1e7)
// }

func indexToDuration(track *cuesheetgo.Track) time.Duration {
	frameTime := time.Duration(float64(track.Index01.Frame) / 75 * float64(time.Second))
	return track.Index01.Timestamp + frameTime
}

// My Python implementation of dbPowerAmp's filename rules
// tr_table = str.maketrans(
//
//	' /:.*\\',          # input characters,
//	'_--__-',           # replaced by these characters,
//	'?,;()!"\'&<>|’¡', # while these characters are simply deleted!
//
// )
var sanitizer *strings.Replacer = strings.NewReplacer(
	" ", "_", ".", "_", "*", "_", "/", "-", ":", "-", "\\", "-",
	"?", "", ",", "", ";", "", "(", "", ")", "", "!", "", "\"", "",
	"'", "", "&", "", "<", "", ">", "", "|", "", "’", "", "¡", "",
)

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

	type badRegion struct {
		start time.Duration
		end   time.Duration
	}
	var badRegions []badRegion

	// TODO: lastGoodSample is more useful (and applicable to both blocking styles), but will be basically unreadable numbers
	var lastGood uint64
	lastGood = 0
	bar := progressbar.Default(int64(stream.Info.NSamples)/int64(stream.Info.SampleRate), "reading")
	for {
		frame, err := stream.ParseNext()
		if err == io.EOF {
			progressbar.Bprintln(bar, "Reached EOF, ending...")
			bar.Finish()
			break
		}
		if err != nil {
			// progressbar.Bprintf(bar, "Found bad frame (lastGood = %d), searching for next good one...\n", lastGood)
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
						// progressbar.Bprintf(bar, "Found good header! (offset: %d) %d\n", byte_offset, inner_frame.Num)

						startSample := lastGood * uint64(stream.Info.BlockSizeMin)
						endSample := (inner_frame.Num - 1) * uint64(stream.Info.BlockSizeMin)
						start := time.Duration(float64(startSample) / float64(stream.Info.SampleRate) * float64(time.Second))
						end := time.Duration(float64(endSample) / float64(stream.Info.SampleRate) * float64(time.Second))
						badRegions = append(badRegions, badRegion{start, end})

						progressbar.Bprintf(bar, "bad region: %s-%s (%s)\n", fmtTime(start), fmtTime(end), fmtTime(end-start))
						// progressbar.Bprintf(bar, "bad region: %s-%s (%s)\n", fmtCDTime(start), fmtCDTime(end), fmtTime(end-start))
						frame = inner_frame
						break
					}
				}
			}
		}
		lastGood = frame.Num
		bar.Set64(int64(frame.SampleNumber() / uint64(frame.SampleRate)))
	}

	cuefile := strings.TrimSuffix(filename, ".flac.part") + ".cue" // TODO: command line arg?
	c, err := os.Open(cuefile)
	if err != nil {
		log.Fatalf("Unable to open cuesheet %s: %s\n", cuefile, err)
	}

	cuesheet, err := cuesheetgo.Parse(c)
	if err != nil {
		log.Fatalln("Unable to parse cuesheet: ", err)
	}

	curBadRegion := 0
	goodTracks := 0
	for t, track := range cuesheet.Tracks {
		trackStart := indexToDuration(track)
		var trackEnd time.Duration
		if t < len(cuesheet.Tracks)-1 {
			trackEnd = indexToDuration(cuesheet.Tracks[t+1])
		} else {
			// Special case for last track
			// May not be entirely accurate due to integer division, but should be
			// close enough for our needs
			trackEnd = time.Duration(stream.Info.NSamples / uint64(stream.Info.SampleRate) * uint64(time.Second))
		}

		for curBadRegion < len(badRegions) {
			badRegionStart := badRegions[curBadRegion].start
			badRegionEnd := badRegions[curBadRegion].end

			if badRegionStart > trackEnd {
				// Track is good, export it
				log.Printf("Track %d good, export %s to %s\n", t+1, fmtTime(trackStart), fmtTime(trackEnd))
				err = exportTrack(filename, track, t, t == len(cuesheet.Tracks)-1, trackStart, trackEnd)
				if err != nil {
					log.Fatalln("Error exporting track: ", err)
				}
				goodTracks++
				break
			} else if badRegionEnd < trackStart {
				// Advance to next bad region (and retest)
				// log.Printf("Bad region %d in the past (ended at %s, track %d starts at %s), advancing...",
				// 	curBadRegion+1, fmtTime(badRegionEnd), t+1, fmtTime(trackStart))
				curBadRegion++
			} else {
				// Track is damaged
				log.Printf("Track %d (%s-%s) damaged by region %d (%s-%s)",
					t+1, fmtTime(trackStart), fmtTime(trackEnd),
					curBadRegion+1, fmtTime(badRegionStart), fmtTime(badRegionEnd))
				break
			}
		}

		if badRegions[len(badRegions)-1].end < trackStart {
			// No more bad regions left, track is good, export it
			log.Printf("Track %d good, export %s to %s\n", t+1, fmtTime(trackStart), fmtTime(trackEnd))
			err = exportTrack(filename, track, t, t == len(cuesheet.Tracks)-1, trackStart, trackEnd)
			if err != nil {
				log.Fatalln("Error exporting track: ", err)
			}
			goodTracks++
		}
	}
	log.Printf("Recovered %d of %d tracks\n", goodTracks, len(cuesheet.Tracks))
}

func exportTrack(original string, track *cuesheetgo.Track, trackNum int, lastTrack bool, start, end time.Duration) error {
	if track.Title == "" {
		return errors.New("no track title")
	}

	args := []string{
		"--best",
		"--verify",
		"--silent",
		// "--warnings-as-errors",
	}

	filename := fmt.Sprintf("%02d-%s.flac", trackNum+1, sanitizer.Replace(track.Title))
	full := filepath.Join(filepath.Dir(original), filename)
	args = append(args, fmt.Sprintf("--output-name=%s", full))
	args = append(args, fmt.Sprintf("--tag=TITLE=\"%s\"", track.Title))
	if track.Performer != "" {
		args = append(args, fmt.Sprintf("--tag=ARTIST=\"%s\"", track.Performer))
	}
	if start > 0 {
		args = append(args, fmt.Sprintf("--skip=%s", fmtTime(start)))
	}
	if !lastTrack {
		args = append(args, fmt.Sprintf("--until=%s", fmtTime(end)))
	}
	args = append(args, original)

	flacOut, err := exec.Command("flac", args...).Output()
	if err != nil {
		exiterror, ok := err.(*exec.ExitError)
		if ok {
			return fmt.Errorf("error running flac (%s): %s", exiterror, exiterror.Stderr)
		} else {
			return fmt.Errorf("error starting flac (%s): %s", err, flacOut)
		}
	}

	return nil
}
