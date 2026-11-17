// Command macawlink is the Windows receiver: it makes an Android phone appear as
// "Macawlink Camera" and "Macawlink Microphone". See docs/ARCHITECTURE.md.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/jose00735/macawlink/windows/internal/config"
	"github.com/jose00735/macawlink/windows/internal/session"
	"github.com/jose00735/macawlink/windows/internal/transport"
)

func main() {
	adb := flag.String("adb", "adb.exe", "path to adb")
	serial := flag.String("serial", "", "phone serial (when several are attached)")
	flag.Parse()

	settings, err := config.Load()
	if err != nil {
		log.Printf("settings: %v (using defaults)", err)
		settings = config.Default()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// TODO(phase1): register virtual camera (vcam) + open virtual mic (vmic) before connecting,
	// so the devices exist even with no phone attached. Start tray UI (ui).

	conn, err := transport.DialUSB(ctx, *adb, *serial)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	s := session.New(conn, settings)

	// TODO(phase1): video.Decoder ← s.Video → vcam.Camera ; audio.Decoder ← s.Audio → vmic.Sink
	if err := s.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("session: %v", err)
	}
}
