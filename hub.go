// Copyright 2015 The GoPivo Authors. All rights reserved.
// Use of this source code is governed by a Simplified BSD
// license that can be found in the LICENSE file.

// Package gopivo provides the base implementation for a hub
// of sockets.
package gopivo

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const defaultJoinLimitRatePerSecond = 64
const defaultJoinLimitRateBurst = 32
const defaultJoinMaxQueueSize = 256

var (
	ErrHubKillWentBad   = errors.New("killing hub raised errors")
	ErrJoinQueueIsFull  = errors.New("join queue is full")
	ErrNoSuchConnector  = errors.New("no such connector")
	ErrPortBufferIsFull = errors.New("port buffer is full")
)

// Connector is the interface that wraps the basic methods needed
// to send and receive the messages to and from a socket.
type Connector interface {
	Error() error
	Initialize() (chan []byte, error)
	RemoteAddr() net.Addr

	Closer(error) error
	Receiver(io.ReadCloser) error
	Sender()
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

// A Hub is a collection of Connectors.
type Hub struct {
	lock     *sync.Mutex
	ports    ports
	queue    chan chan bool
	throttle chan time.Time
}

type ports map[Connector]chan []byte

func NewHub() *Hub {
	h := &Hub{
		lock:     &sync.Mutex{},
		ports:    make(ports),
		queue:    make(chan chan bool, defaultJoinMaxQueueSize),
		throttle: make(chan time.Time, defaultJoinLimitRateBurst),
	}
	go h.run()
	return h
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

func (b *Broadcast) Close() {
	close(b.C)
}

func (h *Hub) run() {
	go h.ticker(defaultJoinLimitRatePerSecond)
	for waiter := range h.queue {
		<-h.throttle
		waiter <- true
	}
}

func (h *Hub) ticker(rate time.Duration) {
	for ns := range time.Tick(time.Second / rate) {
		h.throttle <- ns
	}
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

func (h *Hub) NewBroadcast() *Broadcast {
	bc := &Broadcast{C: make(chan []byte)}
	go bc.broadcast(h)
	return bc
}

func (h *Hub) Join(c Connector, r io.ReadCloser, w Welcomer) error {
	if err := h.waitQueue(); err != nil {
		c.Closer(err)
		return err
	}

	port, err := c.Initialize()
	if err != nil {
		c.Closer(err)
		return err
	}

	go c.Sender()
	if w != nil {
		msg, err := w.Welcome()
		if err != nil {
			c.Closer(err)
			return err
		} else if len(msg) > 0 {
			port <- msg
		}
	}

	h.lock.Lock()
	h.ports[c] = port
	h.lock.Unlock()
	go func() { h.Leave(c, c.Receiver(r)) }()
	return nil
}

func (h *Hub) Kill(reason error) (error, []error) {
	h.lock.Lock()
	defer h.lock.Unlock()
	var errors []error
	for c := range h.ports {
		if err := c.Closer(reason); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return ErrHubKillWentBad, errors
	}
	return nil, nil
}

func (h *Hub) Leave(c Connector, reason error) error {
	h.lock.Lock()
	defer h.lock.Unlock()
	if _, ok := h.ports[c]; ok {
		delete(h.ports, c)
		return c.Closer(reason)
	}
	return ErrNoSuchConnector
}
