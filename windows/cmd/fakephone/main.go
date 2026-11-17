// Command fakephone pretends to be the Macawlink Android app, to test the receiver
// without a phone:
//
//	fakephone -h264 clip.h264            # loop an Annex-B H.264 file + a 440 Hz tone
//	macawlink -addr 127.0.0.1:47800       # in another terminal
//
//	Make a test clip with: ffmpeg -f lavfi -i testsrc2=size=1280x720:rate=30 -t 10 \
//	  -c:v libx264 -profile:v baseline -bf 0 -g 60 -x264-params slices=1 clip.h264
package main

import (
	"flag"
	"log"
	"net"
	"os"

	"github.com/jose00735/macawlink/windows/internal/config"
	"github.com/jose00735/macawlink/windows/internal/fakephone"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:47800", "address to listen on")
	h264 := flag.String("h264", "", "Annex-B H.264 file to loop (optional)")
	model := flag.String("model", "Fake Phone", "model name to report")
	w := flag.Int("w", 1280, "width the clip was encoded at")
	h := flag.Int("h", 720, "height the clip was encoded at")
	fps := flag.Int("fps", 30, "frame rate")
	flag.Parse()

	opt := fakephone.Options{Model: *model, Settings: config.Default(), ToneHz: 440}
	opt.Settings.Video.Width, opt.Settings.Video.Height, opt.Settings.Video.FPS = *w, *h, *fps
	if *h264 != "" {
		b, err := os.ReadFile(*h264)
		if err != nil {
			log.Fatal(err)
		}
		opt.H264 = b
		aus, csd := fakephone.SplitAnnexB(b)
		log.Printf("loaded %d access units, %d bytes of SPS/PPS", len(aus), len(csd))
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("fake phone listening on %s", *listen)
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("receiver connected from %s", c.RemoteAddr())
		go func() {
			err := fakephone.Serve(c, opt, nil, nil)
			log.Printf("receiver disconnected: %v", err)
		}()
	}
}
