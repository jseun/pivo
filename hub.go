// Copyright 2015 The GoPivo Authors. All rights reserved.
// Use of this source code is governed by a Simplified BSD
// license that can be found in the LICENSE file.

// Package gopivo provides the base implementation for a hub
// of sockets.
package gopivo

import (
	"errors"
	"net"
	"sync"
	"time"
)

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
	Receiver(OnReadCloser) error
	RemoteAddr() net.Addr
	Sender() chan []byte
}

// OnCloser is the interface that requires a method to call upon
// disconnection of a connector.
type OnCloser interface {
	OnClose(error) error
}

// OnReader is the interface that wraps the methods called when
// data is read from a connector.
type OnReader interface {
	OnReadBinary([]byte) error
	OnReadText([]byte) error
}

// OnReadCloser wraps the basic methods needed to be notified
// when data has been read or the connector has closed.
type OnReadCloser interface {
	OnCloser
	OnReader
}

// Welcomer is the interface that wraps the method used to
// provide the initial messages to send to the connector
// on a successful join to the hub.
type Welcomer interface {
	Welcome() ([]byte, error)
}

// A Broadcast represents a channel for sending messages
// to all instance of connectors on the hub.
type Broadcast struct {
	C chan []byte
}

// A Hub is a collection of connectors with some specified settings.
type Hub struct {
	lock    *sync.Mutex
	ports   ports
	qslot   chan bool
	queue   chan chan bool
	running bool

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
}

type ports map[Connector]chan []byte

type throttler struct {
	stop chan bool
}

// NewHub instantiate a new hub with default settings.
func NewHub() *Hub {
	return &Hub{
		JoinLimitRateBurst:    DefaultJoinLimitRateBurst,
		JoinLimitRateInterval: DefaultJoinLimitRateInterval,
		JoinMaxQueueSize:      DefaultJoinMaxQueueSize,
	}
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

// Join adds the given connector to the hub.  If a welcomer is
// specified, it sends the provided data through the connector before
// any broadcasted messages get delivered.
//
// The connector's goroutine for receiving and sending messages will
// automatically be started and the given OnReadCloser will get
// notified either if there is data available to read or if the
// connector has been closed.
//
// The caller needs not to worry about disconnecting the connector
// from the hub if remote has closed the connection.  This is done
// and handled by default.
func (h *Hub) Join(c Connector, rc OnReadCloser, w Welcomer) error {
	if err := h.waitQueue(); err != nil {
		c.Close(err)
		return err
	}

	port := c.Sender()
	if w != nil {
		msg, err := w.Welcome()
		if err != nil {
			c.Close(err)
			return err
		} else if len(msg) > 0 {
			port <- msg
		}
	}

	h.lock.Lock()
	h.ports[c] = port
	h.lock.Unlock()
	go func() { h.Leave(c, c.Receiver(rc)) }()
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
	bc := &Broadcast{C: make(chan []byte)}
	go bc.broadcast(h)
	return bc
}

// Start instantiate the hub queues with current settings and
// kicks off the goroutine responsible for throttling the join queue.
func (h *Hub) Start() error {
	if !h.running {
		h.lock = &sync.Mutex{}
		h.ports = make(ports)
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
		close(h.queue)
		return h.Flush(reason)
	}
	return nil, nil
}
