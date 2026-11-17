// Package vmic plays the phone's audio into the virtual microphone's hidden render side.
// See docs/ARCHITECTURE.md §2.2.
package vmic

// Known endpoints, in order of preference.
var Targets = []string{
	"Macawlink Microphone (render)", // Phase 3: own SYSVAD-based driver
	"CABLE Input",                   // Phase 1: VB-Audio Virtual Cable
}

// Sink receives PCM ready for playback (float32 interleaved, endpoint mix format).
type Sink interface {
	// Open finds the first available target endpoint and starts a WASAPI shared-mode render client.
	Open(sampleRate, channels int) (endpointName string, err error)
	Write(pcm []float32) error
	// Silence keeps the endpoint fed with zeros when the phone is gone, so apps never stall.
	Silence()
	Close() error
}
