package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"gitlab.com/gomidi/midi/v2/smf"
)

type playEvent struct {
	absTime float64
	data    smf.Message
}

type player []playEvent

func (p player) Swap(a, b int) {
	p[a], p[b] = p[b], p[a]
}

func (p player) Less(a, b int) bool {
	return p[a].absTime < p[b].absTime
}

func (p player) Len() int {
	return len(p)
}

// Serialize into a byte slice
// 0 - MessageType MidiMessage
// 1-9 - absTime (float64)
// 10- - data
func (p *playEvent) Serialize() []byte {
	// MessageType MidiMessage
	buf := make([]byte, 8+len(p.data))
	binary.BigEndian.PutUint64(buf, math.Float64bits(p.absTime))
	copy(buf[8:], p.data)
	return buf
}

// A tiny process that groups playEvents into larger byte slices
func groupEvents(sendTo chan<- []byte) chan playEvent {
	// Pack messages into 10 kilobyte chunks
	const chunkSize = 10240

	recv := make(chan playEvent)
	go func() {
		chunk := make([]byte, 0, chunkSize)
		chunk = append(chunk, byte(MidiMessage))
		for e := range recv {
			// Serialize the event
			buf := e.Serialize()
			if len(buf) != 11 {
				fmt.Println("buf", buf, "len", len(buf))
				fmt.Println("event type", e.data.Type().String())
				os.Exit(1)
			}
			// If the chunk is full, send it
			if len(chunk)+len(buf) > chunkSize {
				sendTo <- chunk
				chunk = make([]byte, 0, chunkSize)
				chunk = append(chunk, byte(MidiMessage))
			}
			// Append the event to the chunk
			chunk = append(chunk, buf...)
		}

		// Send the last chunk
		if len(chunk) > 1 {
			sendTo <- chunk
		}
	}()
	return recv
}
