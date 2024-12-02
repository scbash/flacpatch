package main

import (
	"io"
	"log"
	"os"

	"github.com/mewkiz/flac"
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

	var lastGood uint64
	lastGood = 0
	for {
		frame, err := stream.ParseNext()
		if err == io.EOF {
			log.Println("Reached EOF, ending...")
			break
		}
		if err != nil {
			log.Printf("Found bad frame (lastGood = %d), searching for next good one...\n", lastGood)
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
						log.Printf("Found good header! (offset: %d) %d\n", byte_offset, inner_frame.Header.Num)
						frame = inner_frame
						break
					}
				}
			}
		}
		lastGood = frame.Num
	}
}
