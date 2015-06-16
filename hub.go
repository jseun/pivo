// Copyright 2015 The Pivo Authors. All rights reserved.
// Use of this source code is governed by a Simplified BSD
// license that can be found in the LICENSE file.

package pivo

import (
	"errors"
	"net"
	"sync"
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

// Error is thrown when disconnecting the connectors from the
// hub have raised some errors.
var ErrHubFlushErrors = errors.New("errors while flushing")

// Error is thrown when the join queue is full.
var ErrJoinQueueIsFull = errors.New("join queue is full")

// Error is thrown when a connection is not connected to the hub.
var ErrNoSuchConnector = errors.New("no such connector")

// Error is thrown when a connector has its port buffer full.
var ErrPortBufferIsFull = errors.New("port buffer is full")

// Connector is the interface that wraps the basic methods needed
// to send and receive the messages to and from a socket.
type Connector interface {
	Close(error) error
	Receiver(OnBinaryReader, OnTextReader, OnCloser) error
	RemoteAddr() net.Addr
	Sender() Port
}

// OnBinaryReader is called when binary data is read from
// a connector.
type OnBinaryReader interface {
	OnBinaryRead([]byte) error
}

// OnBinaryReadCloser is the interface that wraps basic methods
// both for reading binary data and getting notified of a closing
// connector.
type OnBinaryReadCloser interface {
	OnBinaryReader
	OnCloser
}

// OnCloser is the interface that requires a method to call upon
// disconnection of a connector.
type OnCloser interface {
	OnClose(error) error
}

// OnJoiner is the interface that requires a method to call upon
// attempt to join the hub.  Implementation will get the connector
// as argument.  Any error returned will prevent the join to complete
// and a message, if not nil, will be sent to all connected connectors.
type OnJoiner interface {
	OnJoin(Connector, Port) (*Message, error)
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
// both for reading text data and getting notified of a closing
// connector.
type OnTextReadCloser interface {
	OnTextReader
	OnCloser
}

// A Broadcast represents a channel for sending messages
// to all instance of connectors on the hub.
type Broadcast struct {
	C Port
}

// A Hub is a collection of connectors with some specified settings.
type Hub struct {
	// Joiner is the implementor of the OnJoiner interface.
	Joiner OnJoiner

	// The interval at which new connections are processed.
	// A Duration of time.Second / 100 would mean hundred
	// of new connections are processed in a second.
	JoinLimitRateInterval time.Duration

	// The size of the burst allowed when the join limit
	// interval is reached. A value of 10 would mean that
	// 10 more new connections may stop waiting to join
	// the hub,
	JoinLimitRateBurst uint

	// The maximum number of new connections waiting to
	// join the hub before ErrJoinQueueIsFull is being
	// returned.
	JoinMaxQueueSize uint

	bc      *Broadcast     // Hub's own broadcast channel
	lock    *sync.Mutex    // Connectors lock
	ports   Ports          // Map of connected connectors
	qslot   chan bool      // Join burst channel
	queue   chan chan bool // The join queue
	running bool           // Hub is running or not
}

// Message is a data structure containing both the type of data
// and the data itself.
type Message struct {
	Data []byte
	Type int
}

// Port is a channel transporting a pointer to a Message.
type Port chan *Message

// Ports is a map of Port indexed by Connector.
type Ports map[Connector]Port

type throttler struct {
	stop chan bool
}

// BinaryMessage formats a binary message and returns
// a pointer to it.
func BinaryMessage(bin []byte) *Message {
	return &Message{Data: bin, Type: IsBinaryMessage}
}

// NewHub instantiate a new hub with default settings.
func NewHub() *Hub {
	return &Hub{
		JoinLimitRateBurst:    DefaultJoinLimitRateBurst,
		JoinLimitRateInterval: DefaultJoinLimitRateInterval,
		JoinMaxQueueSize:      DefaultJoinMaxQueueSize,
	}
}

// TextMessage formats a text message and returns
// a pointer to it.
func TextMessage(text string) *Message {
	return &Message{Data: []byte(text), Type: IsTextMessage}
}

func (b *Broadcast) broadcast(h *Hub) {
	for msg := range b.C {
		h.lock.Lock()
		for c, port := range h.ports {
			select {
			case port <- msg:
			default:
				err := ErrPortBufferIsFull
				go h.Leave(c, err)
			}
		}
		h.lock.Unlock()
	}
}

// Close should be called when a broadcast channel is no longer
// used.  Both the created channel and goroutine will be taken down.
func (b *Broadcast) Close() {
	close(b.C)
}

func (h *Hub) run() {
	h.running = true
	throttler := h.throttler(h.JoinLimitRateInterval)
	defer func() {
		h.running = false
		throttler.stop <- true
	}()

	for waiter := range h.queue {
		waiter <- <-h.qslot
	}
}

func (h *Hub) throttler(rate time.Duration) *throttler {
	throttler := &throttler{stop: make(chan bool)}
	go func() {
		ticker := time.NewTicker(rate)
		defer func() { ticker.Stop(); close(h.qslot) }()
		for {
			select {
			case <-throttler.stop:
				return
			case <-ticker.C:
				select {
				case h.qslot <- true:
				default:
				}
			}
		}
	}()
	return throttler
}

func (h *Hub) waitQueue() error {
	waiter := make(chan bool)
	defer close(waiter)

	select {
	case h.queue <- waiter:
	default:
		return ErrJoinQueueIsFull
	}
	<-waiter
	return nil
}

// Flush disconnects all connectors from the hub without actually
// stopping it.
func (h *Hub) Flush(reason error) (error, []error) {
	h.lock.Lock()
	defer h.lock.Unlock()
	var errors []error
	for c := range h.ports {
		if err := c.Close(reason); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return ErrHubFlushErrors, errors
	}
	return nil, nil
}

// Join adds the given connector to the hub.  If a joiner is
// specified, it is given a chance to send data to the peer
// (although reading is not allowed at this stage).  The joiner
// may also choose to not allow the connector to join the hub.
//
// The connector's goroutine for receiving and sending messages will
// automatically be started and the given OnReadCloser will get
// notified either if there is data available to read or if the
// connector has been closed.  At this stage, reading from the
// socket is allowed.
//
// The caller needs not to worry about disconnecting the connector
// from the hub if remote has closed the connection.  This is done
// and handled by default.
func (h *Hub) Join(c Connector, rc OnReadCloser) error {
	if err := h.waitQueue(); err != nil {
		c.Close(err)
		return err
	}

	// Get a port to send data to
	port := c.Sender()
	if h.Joiner != nil {
		msg, err := h.Joiner.OnJoin(c, port)
		if err != nil {
			// Joiner chose to deny access to the hub
			c.Close(err)
			if msg != nil {
				// Broadcast notice to others
				h.bc.C <- msg
			}
			return err
		}
	}

	h.lock.Lock()
	h.ports[c] = port
	h.lock.Unlock()
	go func() { h.Leave(c, c.Receiver(rc, rc, rc)) }()
	return nil
}

// Leave disconnects the given connector from the hub.
// If reason is not nil, the remote end shall receive the reason.
// It is up to the connector to deliver the reason by its own mean.
func (h *Hub) Leave(c Connector, reason error) error {
	h.lock.Lock()
	defer h.lock.Unlock()
	if port, ok := h.ports[c]; ok {
		delete(h.ports, c)
		close(port)
		return c.Close(reason)
	}
	return ErrNoSuchConnector
}

// NewBroadcast gives the caller a new broadcast channel
// and kicks off the goroutine responsible for delivering
// broadcasted message to the connectors currently connected.
func (h *Hub) NewBroadcast() *Broadcast {
	bc := &Broadcast{C: make(Port)}
	go bc.broadcast(h)
	return bc
}

// Start instantiate the hub queues with current settings and
// kicks off the goroutine responsible for throttling the join queue.
func (h *Hub) Start() error {
	if !h.running {
		h.bc = h.NewBroadcast()
		h.lock = &sync.Mutex{}
		h.ports = make(Ports)
		h.queue = make(chan chan bool, h.JoinMaxQueueSize)
		h.qslot = make(chan bool, h.JoinLimitRateBurst)
		go h.run()
	}
	return nil
}

// Stop closes the join queue and disconnect all connectors from
// the hub.  The throttling goroutine will also go down.
func (h *Hub) Stop(reason error) (error, []error) {
	if h.running {
		h.bc.Close()
		close(h.queue)
		return h.Flush(reason)
	}
	return nil, nil
}
