// Command devreceiver runs the Macawlink receiver logic without Windows (no camera, no audio
// device) next to an in-process fake phone that announces the teleprompter feature. It serves
// the real settings API on 127.0.0.1:47801, so TeleCyborg's phone feed can be tested end to end
// on any OS. Prompter packets the "phone" receives are printed to stdout.
//
//	go run ./cmd/devreceiver [-tap next]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jose00735/macawlink/windows/internal/app"
	"github.com/jose00735/macawlink/windows/internal/config"
	"github.com/jose00735/macawlink/windows/internal/fakephone"
	"github.com/jose00735/macawlink/windows/internal/native"
	"github.com/jose00735/macawlink/windows/internal/protocol"
	"github.com/jose00735/macawlink/windows/internal/web"
)

type nopDecoder struct{}

func (nopDecoder) Push([]byte, int64) (bool, error)           { return false, nil }
func (nopDecoder) Pull([]byte) (int, int, int64, bool, error) { return 0, 0, 0, false, nil }
func (nopDecoder) Close()                                     {}

type nopAudio struct{}

func (nopAudio) Write(_ []int16, n int) (int, error) { return n, nil }
func (nopAudio) Queued() int                         { return 0 }
func (nopAudio) Close()                              {}
func (nopAudio) Endpoint() string                    { return "none" }

func main() {
	addr := flag.String("web", web.DefaultAddr, "settings API address")
	tap := flag.String("tap", "", "after the first script, the phone taps this key (toggle, next, …)")
	flag.Parse()
	log.SetOutput(os.Stderr)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	rec := &fakephone.Received{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			fakephone.Serve(c, fakephone.Options{Model: "Dev Phone", Settings: config.Default(), AudioOnly: true,
				Features: []string{protocol.FeaturePrompter}, PrompterKey: *tap}, rec, make(chan struct{}))
		}
	}()
	go func() { // print what the phone gets
		seen := [3]int{}
		for {
			time.Sleep(100 * time.Millisecond)
			sc, st, cl := rec.Prompter()
			for ; seen[0] < len(sc); seen[0]++ {
				fmt.Printf("SCRIPT %s\n", sc[seen[0]])
			}
			for ; seen[1] < len(st); seen[1]++ {
				fmt.Printf("STATE %s\n", st[seen[1]])
			}
			for ; seen[2] < cl; seen[2]++ {
				fmt.Println("CLEAR")
			}
		}
	}()
	a := app.New(app.Options{Addr: ln.Addr().String(),
		NewDecoder: func(int) (native.Decoder, error) { return nopDecoder{}, nil },
		OpenAudio:  func(int, int) (native.AudioOut, error) { return nopAudio{}, nil },
	}, config.Default())
	go a.Run(context.Background())
	fmt.Println("READY", *addr)
	log.Fatal(http.ListenAndServe(*addr, web.Handler(a)))
}
