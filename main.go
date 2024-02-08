package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.com/gomidi/midi/v2"
	"gitlab.com/gomidi/midi/v2/smf"
)

var hub = Hub{
	clients:    make(map[*Client]struct{}),
	register:   make(chan *Client),
	unregister: make(chan *Client),
	broadcast:  make(chan Message),
}

type MessageType byte

const (
	TimeSync    MessageType = 0
	MidiMessage MessageType = 1
)

// Message struct is used to send messages between clients
// All messages need a timestamp of _when they were created_.
//
// For midi playback we want to keep a consistent 100ms delay from the server calling "play"
// to the client actually playing the note.
//
// 0-7: Timestamp
// 8:   MessageType
// 9-:  Content
type Message struct {
	Timestamp uint64
	Type      MessageType
	Content   midi.Message
}

// Creates a byte slice from a message
func (m Message) Bytes() []byte {
	b := make([]byte, 9)
	binary.BigEndian.PutUint64(b, m.Timestamp)
	b[8] = byte(m.Type)
	return slices.Concat(b, m.Content.Bytes())
}

type Hub struct {
	// Registered clients.
	clients    map[*Client]struct{}
	register   chan *Client
	unregister chan *Client
	broadcast  chan Message
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
			msg := Message{
				Timestamp: uint64(time.Now().UnixMilli()),
				Type:      TimeSync,
			}.Bytes()

			for client := range h.clients {
				client.send <- msg
			}
		case message := <-h.broadcast:
			msg := message.Bytes()
			for client := range h.clients {
				client.send <- msg
			}
			ticker.Reset(timeSyncInterval)
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

			log.Println("Sending message to client")
			c.conn.WriteMessage(websocket.BinaryMessage, message)
		}
	}
}

func (h Hub) Open() error {
	return nil
}

func (h Hub) Close() error {
	return nil
}

func (h Hub) IsOpen() bool {
	return true
}

func (h Hub) Number() int {
	return 424242
}

func (h Hub) String() string {
	return "Websocket Hub"
}

func (h Hub) Underlying() interface{} {
	return nil
}

func (h Hub) Send(data []byte) error {
	h.broadcast <- Message{
		Timestamp: uint64(time.Now().UnixMilli()),
		Type:      MidiMessage,
		Content:   midi.Message(data),
	}
	return nil
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
		// open queen.mid
		file, err := os.Open("queen.mid")
		if err != nil {
			log.Fatal(err)
		}

		// wait for user input
		fmt.Println("Press enter to start playback")
		fmt.Scanln()

		smf.ReadTracksFrom(file).
			Do(
				func(te smf.TrackEvent) {
					if te.Message.IsMeta() {
						fmt.Printf("[%v] @%vms %s\n", te.TrackNo, te.AbsMicroSeconds/1000, te.Message.String())
					} else {
						fmt.Printf("[%v] %s\n", te.TrackNo, te.Message)
					}
				},
			).Play(hub)
	}()

	go hub.run()

	log.Println("Server started on: http://localhost")
	http.ListenAndServe(":80", nil)
}
