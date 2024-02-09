package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.com/gomidi/midi/v2"
	"gitlab.com/gomidi/midi/v2/smf"
)

var hub = Hub{
	clients:    make(map[*Client]struct{}),
	register:   make(chan *Client),
	unregister: make(chan *Client),
	broadcast:  make(chan []byte),
	created:    time.Now(),
}

type MessageType byte

const (
	TimeSync      MessageType = 0
	MidiMessage   MessageType = 1
	ProgramChange MessageType = 2
	StartEvent    MessageType = 3
	StopEvent     MessageType = 4
)

func NewTimeSyncMessage(t float64) []byte {
	// Create a byte slice from a time.Duration
	var buf [9]byte
	buf[0] = byte(TimeSync)
	binary.BigEndian.PutUint64(buf[1:], math.Float64bits(t))
	return buf[:]
}

func NewProgramChangeMessage(t float64, b []byte) []byte {
	var buf [9]byte
	buf[0] = byte(ProgramChange)
	binary.BigEndian.PutUint64(buf[1:], math.Float64bits(t))
	return append(buf[:], b...)
}

func NewStartEventMessage(t float64) []byte {
	// 0 - MessageType StartEvent
	// 1-9 - start time (float64)
	var buf [9]byte
	buf[0] = byte(StartEvent)
	binary.BigEndian.PutUint64(buf[1:], math.Float64bits(t))
	return buf[:]
}

func NewStopEventMessage() []byte {
	// 0 - MessageType StopEvent
	return []byte{byte(StopEvent)}
}

type Hub struct {
	// Registered clients.
	clients    map[*Client]struct{}
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte

	// Creation time
	created time.Time
}

// MonotonicTime returns the time since the hub was created in milliseconds
func (h *Hub) MonotonicTime() float64 {
	return float64(time.Now().Sub(h.created).Milliseconds())
}

func (h *Hub) run() {
	// Periodically send the current time to all clients
	const timeSyncInterval = 1000 * time.Millisecond
	ticker := time.NewTicker(timeSyncInterval)

	for {
		select {
		case client := <-h.register:
			h.clients[client] = struct{}{}
		case client := <-h.unregister:
			delete(h.clients, client)
		case <-ticker.C:
			msg := NewTimeSyncMessage(h.MonotonicTime())
			for client := range h.clients {
				client.send <- msg
			}
		case message := <-h.broadcast:
			msg := message
			for client := range h.clients {
				client.send <- msg
			}
		}
	}
}

// Websocket client implementation
//
// Clients need to keep track of their time-delta from the server.
// So the server ensures that it sends the current time to the client at least once every 5 seconds.
type Client struct {
	// The websocket connection.
	conn *websocket.Conn
	send chan []byte
}

func (c *Client) CloseConn() {
	c.conn.Close()
}

// writePump pumps messages from the hub to the websocket connection.
func (c *Client) writePump() {
	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.CloseConn()
				return
			}
			c.conn.WriteMessage(websocket.BinaryMessage, message)
		}
	}
}

// Handle websocket connections
func wsHandler(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{}

	// Upgrade the HTTP connection to a websocket connection
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	client := &Client{conn: conn, send: make(chan []byte, 256)}
	hub.register <- client
	go client.writePump()
}

func main() {
	// Serve index.html
	http.Handle("/", http.FileServer(http.Dir("static")))

	// Handle websocket connections
	http.HandleFunc("/ws", wsHandler)

	// Broadcast a Simple MIDI Format file after waiting for command-line input
	go func() {
		file, err := os.Open("rushE.mid")
		if err != nil {
			log.Fatal(err)
		}

		// wait for user input
		fmt.Println("Press enter to start playback")
		fmt.Scanln()

		grouper := groupEvents(hub.broadcast)

		// Preload midi events
		smf.ReadTracksFrom(file).Do(func(te smf.TrackEvent) {
			msg := te.Message
			if msg.IsPlayable() {
				time := float64(te.AbsMicroSeconds) / 1000.0

				if msg.Type().Is(midi.ProgramChangeMsg) {
					// Program change messages are handled differently
					hub.broadcast <- NewProgramChangeMessage(time, msg.Bytes())
				} else {
					grouper <- playEvent{
						absTime: time,
						data:    msg.Bytes(),
					}
				}
			}
		})
		close(grouper)

		// Wait 5 seconds before starting playback
		time.Sleep(5 * time.Second)

		// Broadcast a start event to begin playback after 5 seconds
		hub.broadcast <- NewStartEventMessage(hub.MonotonicTime() + 5000)

		// Countdown on the console
		for i := 5; i > 0; i-- {
			fmt.Println(i)
			time.Sleep(1 * time.Second)
		}

		// Wait for user input to stop playback
		fmt.Println("Press enter to stop playback")
		fmt.Scanln()

		// Broadcast a stop event to end playback
		hub.broadcast <- NewStopEventMessage()
	}()

	go hub.run()

	log.Println("Server started on: http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}
