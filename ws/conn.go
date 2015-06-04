// Copyright 2015 The Pivo Authors. All rights reserved.
// Use of this source code is governed by a Simplified BSD
// license that can be found in the LICENSE file.

package ws

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/pivo.v1"
)

// Default ping timeout value in seconds
const DefaultPingTimeout = 60

// Default port buffer size
const DefaultPortBufferSize = 64

// Default read buffer size
const DefaultReadBufferSize = 1024

// Default write buffer size
const DefaultWriteBufferSize = 1024

// Default write timeout value in seconds
const DefaultWriteTimeout = 10

// Conn specifies parameters for this connector.
type Conn struct {
	port    chan []byte
	timeout time.Duration
	ws      *websocket.Conn

	// Ping timeout
	PingTimeout time.Duration

	// Write timeout
	WriteTimeout time.Duration

	// Port buffer size
	PortBufferSize int

	// Those settings are only used by upgraders.
	// See http://godoc.org/github.com/gorilla/websocket#Upgrader
	CheckOrigin     func(*http.Request) bool
	ReadBufferSize  int
	WriteBufferSize int
}

// NewConn instantiate a connector with default settings.
func NewConn() *Conn {
	return &Conn{
		PingTimeout:     DefaultPingTimeout,
		PortBufferSize:  DefaultPortBufferSize,
		ReadBufferSize:  DefaultReadBufferSize,
		WriteBufferSize: DefaultWriteBufferSize,
		WriteTimeout:    DefaultWriteTimeout,
	}
}

func (c *Conn) ping() error {
	return c.write(websocket.PingMessage, []byte{})
}

func (c *Conn) send(buf []byte) (err error) {
	return c.write(websocket.TextMessage, buf)
}

func (c *Conn) write(t int, buf []byte) error {
	c.ws.SetWriteDeadline(time.Now().Add(c.WriteTimeout * time.Second))
	return c.ws.WriteMessage(t, buf)
}

// Close sends a closure message to the remote end.
func (c *Conn) Close(err error) error {
	code := websocket.CloseNormalClosure
	msg := websocket.FormatCloseMessage(code, fmt.Sprint(err))
	wait := time.Now().Add(c.WriteTimeout * time.Second)
	return c.ws.WriteControl(websocket.CloseMessage, msg, wait)
}

// Dial opens a connection to the given URL with the provided header.
func (c *Conn) Dial(url string, h http.Header) (*Conn, *http.Response, error) {
	var dialer = &websocket.Dialer{}
	ws, r, err := dialer.Dial(url, h)
	if err != nil {
		return nil, r, err
	}
	c.ws = ws
	return c, r, nil
}

// Receiver is an event loop that either calls OnCloser if the connection
// has terminated or OnReader when data has been read from the socket.
func (c *Conn) Receiver(rc pivo.OnReadCloser) error {
	defer c.ws.Close()
	timeout := c.PingTimeout * time.Second
	c.ws.SetReadDeadline(time.Now().Add(timeout))
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(timeout))
		return nil
	})

	for {
		msgt, data, err := c.ws.ReadMessage()
		switch {
		case err == io.EOF:
			return rc.OnClose(nil)
		case err != nil:
			return rc.OnClose(err)
		case msgt == websocket.BinaryMessage:
			if err := rc.OnReadBinary(data); err != nil {
				return rc.OnClose(err)
			}
		case msgt == websocket.TextMessage:
			if err := rc.OnReadText(data); err != nil {
				return rc.OnClose(err)
			}
		}
	}
}

// RemoteAddr returns the IP address of the remote end.
func (c *Conn) RemoteAddr() net.Addr {
	return c.ws.RemoteAddr()
}

// Sender kicks off a goroutine reading from the returned channel
// and writing the bytes harvested through the socket. The goroutine
// will run until one of the following conditions are met:
//
// Either the returned channel has been closed,
// an error occured writing on the socket or
// a ping timeout occured.
//
// Sender can only send TextMessage at this time.
func (c *Conn) Sender() chan []byte {
	c.port = make(chan []byte, c.PortBufferSize)
	pingInterval := (9 * c.PingTimeout * time.Second) / 10
	pinger := time.NewTicker(pingInterval)
	go func() {
		defer pinger.Stop()
		for {
			select {
			case msg, ok := <-c.port:
				if !ok {
					return
				}

				if err := c.send(msg); err != nil {
					c.ws.Close()
					return
				}
			case <-pinger.C:
				if err := c.ping(); err != nil {
					c.ws.Close()
					return
				}
			}
		}
	}()
	return c.port
}

// Upgrade tries to upgrade an HTTP request to a Websocket session.
func (c *Conn) Upgrade(w http.ResponseWriter, r *http.Request, h http.Header) error {
	upgrader := &websocket.Upgrader{
		CheckOrigin:     c.CheckOrigin,
		ReadBufferSize:  c.ReadBufferSize,
		WriteBufferSize: c.WriteBufferSize,
	}

	ws, err := upgrader.Upgrade(w, r, h)
	if err != nil {
		return err
	}
	c.ws = ws
	return nil
}
