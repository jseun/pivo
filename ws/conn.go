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
	"gopkg.in/pivo.v2"
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

	port    pivo.Port       // Messages to write to the socket
	timeout time.Duration   // Ping timeout duration
	ws      *websocket.Conn // Websocket connection
}

// DefaultConn instantiate a connector with default settings.
func DefaultConn() *Conn {
	return &Conn{
		PingTimeout:     DefaultPingTimeout,
		PortBufferSize:  DefaultPortBufferSize,
		ReadBufferSize:  DefaultReadBufferSize,
		WriteBufferSize: DefaultWriteBufferSize,
		WriteTimeout:    DefaultWriteTimeout,
	}
}

// ping writes a PingMessage to the socket.
func (c *Conn) ping() error {
	return c.write(websocket.PingMessage, []byte{})
}

// send writes either binary or text message to the socket.
func (c *Conn) send(msg *pivo.Message) error {
	switch msg.Type {
	case pivo.IsBinaryMessage:
		return c.write(websocket.BinaryMessage, msg.Data)
	case pivo.IsTextMessage:
		// Default is to send text message
	}
	return c.write(websocket.TextMessage, msg.Data)
}

// write writes to the socket and controls the write deadline.
func (c *Conn) write(t int, buf []byte) error {
	c.ws.SetWriteDeadline(time.Now().Add(c.WriteTimeout * time.Second))
	return c.ws.WriteMessage(t, buf)
}

// BinaryMessage formats a binary message and returns a pointer to it.
func (c *Conn) BinaryMessage(bin []byte) *pivo.Message {
	return pivo.BinaryMessage(c, bin)
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

// Protocol returns the name of the Pivo transport protocol used.
func (c *Conn) Protocol() string {
	return "websocket"
}

// Receiver is an event loop that either calls OnCloser if the connection
// has terminated or OnReader when data has been read from the socket.
// Reading data from the socket or keeping it alive will not work unless
// that function is spinning in a goroutine.
//
// Receiver returns whatever error the OnCloser did return.
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

		// Remote closed connection as expected
		case err == io.EOF:
			return rc.OnClose(nil)

		// Remote closed connection unexpectedly
		case err != nil:
			return rc.OnClose(err)

		// Binary data has been read
		case msgt == websocket.BinaryMessage:
			err := rc.OnBinaryRead(data)
			if err != nil {
				return rc.OnClose(err)
			}

		// Text data has been read
		case msgt == websocket.TextMessage:
			err := rc.OnTextRead(string(data))
			if err != nil {
				return rc.OnClose(err)
			}

		}
	}
	return pivo.ErrShouldNotReachThis
}

// RemoteAddr returns the IP address of the remote end.
func (c *Conn) RemoteAddr() net.Addr {
	return c.ws.RemoteAddr()
}

// Send pushes the given message through the port buffer.
// pivo.ErrPortBufferIsFull is thrown either if the port
// buffer is not open or if it has reached it's buffer size.
func (c *Conn) Send(m *pivo.Message) error {
	select {
	case c.port <- m:
	default:
		return pivo.ErrPortBufferIsFull
	}
	return nil
}

// Sender reads messages from the port buffer and write them
// to the socket.  Sending data or keeping the socket open
// will not be possible without spinning that function in a
// goroutine.
func (c *Conn) Sender() error {
	c.port = make(pivo.Port, c.PortBufferSize)
	pingInterval := (9 * c.PingTimeout * time.Second) / 10
	pinger := time.NewTicker(pingInterval)
	defer func() { pinger.Stop(); c.ws.Close() }()
	for {
		select {

		// Send
		case msg := <-c.port:
			if err := c.send(msg); err != nil {
				return err
			}

		// Ping
		case <-pinger.C:
			if err := c.ping(); err != nil {
				return err
			}

		}
	}
	return pivo.ErrShouldNotReachThis
}

// TextMessage formats a text message and returns a pointer to it.
func (c *Conn) TextMessage(text string) *pivo.Message {
	return pivo.TextMessage(c, text)
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
