// Copyright 2015 The Pivo Authors. All rights reserved.
// Use of this source code is governed by a Simplified BSD
// license that can be found in the LICENSE file.

package pivo

import (
	"errors"
	"net"
	"time"
)

// The Pivo package version numbers
const Version = "2.0.0"

// Default value for the join rate limit.
// Currently defined as a maximum of 64 per seconds.
const DefaultJoinLimitRateInterval = time.Second / 64

// Default value for the allowed burst over the join limit.
// Currently defined as a maximum of 32 more connections.
const DefaultJoinLimitRateBurst = 32

// Default value for the size of the join queue.
// Currently defined as 256 pending connections before new
// ones are discarded.
const DefaultJoinMaxQueueSize = 256

// Marker for binary message type
const IsBinaryMessage = 1

// Marker for text message type
const IsTextMessage = 0

// Error is thrown when the join queue is full.
var ErrJoinQueueIsFull = errors.New("join queue is full")

// Error is thrown when a connector has its port buffer full.
var ErrPortBufferIsFull = errors.New("port buffer is full")

// Error is thrown when unexpected code path is reached.
var ErrShouldNotReachThis = errors.New("should not reach this")

// Connector is the interface that wraps the basic methods needed
// to use a connector with the hub.
type Connector interface {
	Close(error) error
	Protocol() string
	RemoteAddr() net.Addr
	Send(*Message) error
}

// OnBinaryReader is called when binary data is read from
// a connector.
type OnBinaryReader interface {
	OnBinaryRead([]byte) error
}

// OnBinaryReadCloser is the interface that wraps basic methods
// both for reading binary data and getting notified about a
// closed connection.
type OnBinaryReadCloser interface {
	OnBinaryReader
	OnCloser
}

// OnCloser is the interface that requires a method to call upon
// disconnection of a connector.
type OnCloser interface {
	OnClose(error) error
}

// OnReader is the interface that wraps the methods called when
// data is read from a connector.
type OnReader interface {
	OnBinaryReader
	OnTextReader
}

// OnReadCloser wraps the basic methods needed to be notified
// when data has been read or the connector has closed.
type OnReadCloser interface {
	OnCloser
	OnReader
}

// OnTextReader is called when text data is read from
// a connector.
type OnTextReader interface {
	OnTextRead(string) error
}

// OnTextReadCloser is the interface that wraps basic methods
// both for reading text data and getting notified about a
// closed connection.
type OnTextReadCloser interface {
	OnTextReader
	OnCloser
}

// A Hub is a collection of connectors with some specified settings.
type Hub struct {
	// The interval at which new connections are processed.
	// A Duration of time.Second / 100 would mean hundred
	// of new connections are processed in a second.
	JoinLimitRateInterval time.Duration

	// The size of the burst allowed when the join rate limit
	// is reached. A value of 10 would mean that 10 extra
	// connections may wait to join the hub.
	JoinLimitRateBurst uint

	// The maximum number of new connections waiting to
	// join the hub before ErrJoinQueueIsFull is being
	// thrown.
	JoinMaxQueueSize uint

	broadcast Port           // Broadcast channel
	join      chan Connector // Join the connector
	leave     chan Connector // Disjoin the connector
	ports     Ports          // Map of connected connectors
	stop      chan bool      // Stop the hub
	timeslots chan bool      // Join burst timeslots
}

// OnHubBroadcaster is triggered before the message is delivered.
// Callee may alter the message in any way as well as
// provide a different list of recipients ports.
// If error is thrown, broadcast will not occur at all.
type OnHubBroadcaster interface {
	OnHubBroadcast(*Message) (Ports, error)
}

// OnHubJoiner is triggered before a connector may join the hub.
// If error is thrown, join will not occur at all.
type OnHubJoiner interface {
	OnHubJoin(Connector) error
}

// OnHubLeaver is triggered before a connector disjoin the hub.
// Nothing may prevent this from happening at this stage.
type OnHubLeaver interface {
	OnHubLeave(Connector)
}

// OnHubCloser is triggered right before the hub stops.
// The map of connected connectors is provided and is
// guaranteed to be accurate at the time the callee has it.
type OnHubCloser interface {
	OnHubClose(Ports)
}

// Message is a data structure with bytes of data, type of data,
// and its original connector.
type Message struct {
	Data []byte    // Bytes of data
	From Connector // Original connector
	Type int       // Type of data
}

// Port is a channel transporting a pointer to a Message.
type Port chan *Message

// Ports is a map of Connectors that have joined the hub.
type Ports map[Connector]bool

// throttler is the timeslots ticker.
type throttler struct {
	stop chan bool // Stop the throttler
}

// BinaryMessage formats a binary message and returns
// a pointer to it.
func BinaryMessage(from Connector, bin []byte) *Message {
	return &Message{bin, from, IsBinaryMessage}
}

// DefaultHub instantiate a new hub with default settings.
func DefaultHub() *Hub {
	return &Hub{
		JoinLimitRateBurst:    DefaultJoinLimitRateBurst,
		JoinLimitRateInterval: DefaultJoinLimitRateInterval,
		JoinMaxQueueSize:      DefaultJoinMaxQueueSize,
	}
}

// TextMessage formats a text message and returns
// a pointer to it.
func TextMessage(from Connector, text string) *Message {
	return &Message{[]byte(text), from, IsTextMessage}
}

// throttler fills the timeslots channel at specified interval.
func (h *Hub) throttler(rate time.Duration) *throttler {
	throttler := &throttler{stop: make(chan bool)}
	go func() {
		h.timeslots = make(chan bool, h.JoinLimitRateBurst+1)
		ticker := time.NewTicker(rate)
		defer func() { ticker.Stop(); close(h.timeslots) }()
		for {
			select {
			case <-throttler.stop:
				return
			case <-ticker.C:
				select {
				case h.timeslots <- true:
				default:
				}
			}
		}
	}()
	return throttler
}

// Broadcast sends a message to all connectors on the hub.
func (h *Hub) Broadcast(m *Message) { h.broadcast <- m }

// Leave disjoins the given connector from the hub.
func (h *Hub) Leave(c Connector) { h.leave <- c }

// Stop brings the hub down.
func (h *Hub) Stop() { h.stop <- true }

// Join adds the given connector to the hub.
func (h *Hub) Join(c Connector) error {
	select {
	case h.join <- c:
	default:
		return ErrJoinQueueIsFull
	}
	return nil
}

// StartAndServe serves the hub until Stop() is requested.
func (h *Hub) StartAndServe(trigger interface{}) error {
	var throttler *throttler

	// Start a throttler if necessary
	if h.JoinLimitRateInterval > 0 {
		throttler = h.throttler(h.JoinLimitRateInterval)
		defer func() { throttler.stop <- true }()
	}

	// Initialize control channels
	h.broadcast = make(Port)
	h.join = make(chan Connector, h.JoinMaxQueueSize)
	h.leave = make(chan Connector)
	h.stop = make(chan bool)

	// Initialize ports map
	h.ports = Ports{}

	// Loop until Stop()
	for {
		select {

		// Broadcast
		case msg := <-h.broadcast:
			var recipients = h.ports

			// OnHubBroadcaster event trigger
			if t, ok := trigger.(OnHubBroadcaster); ok {
				ports, err := t.OnHubBroadcast(msg)
				if err != nil {
					continue
				} else if ports != nil {
					recipients = ports
				}
			}

			// Send message to the recipients
			for conn, _ := range recipients {
				if msg.From == conn {
					// Skip original connector
					continue
				} else if err := conn.Send(msg); err != nil {
					conn.Close(err)
					delete(h.ports, conn)
					// OnHubLeaver event trigger
					if t, ok := trigger.(OnHubLeaver); ok {
						t.OnHubLeave(conn)
					}
				}
			}

		// Join
		case conn := <-h.join:
			// Throttle
			if throttler != nil {
				<-h.timeslots
			}

			// OnHubJoiner event trigger
			if t, ok := trigger.(OnHubJoiner); ok {
				if t.OnHubJoin(conn) != nil {
					continue
				}
			}

			h.ports[conn] = true

		// Leave
		case conn := <-h.leave:
			delete(h.ports, conn)
			// OnHubLeaver event trigger
			if t, ok := trigger.(OnHubLeaver); ok {
				t.OnHubLeave(conn)
			}

		// Stop
		case <-h.stop:
			// OnHubCloser event trigger
			if t, ok := trigger.(OnHubCloser); ok {
				t.OnHubClose(h.ports)
			}
			return nil

		}
	}
	return ErrShouldNotReachThis
}
