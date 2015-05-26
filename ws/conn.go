// Copyright 2015 The GoPivo Authors. All rights reserved.
// Use of this source code is governed by a Simplified BSD
// license that can be found in the LICENSE file.

package ws

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultBacklogSize = 64
	defaultPingTimeout = 60 * time.Second

	writeWaitTime = 10 * time.Second
)

var ErrReceiverHasGoneAway = errors.New("receiver has gone away")

var upgrader = &websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Conn struct {
	backlog int
	output  chan []byte

	pingTimeout time.Duration

	err      error
	isClosed bool
	leave    chan []byte
	ws       *websocket.Conn
}

func NewConn() *Conn {
	return &Conn{
		backlog:     defaultBacklogSize,
		isClosed:    false,
		leave:       make(chan []byte),
		pingTimeout: defaultPingTimeout,
		ws:          &websocket.Conn{},
	}
}

func (c *Conn) ping() error {
	return c.write(websocket.PingMessage, []byte{})
}

func (c *Conn) send(buf []byte) (err error) {
	return c.write(websocket.TextMessage, buf)
}

func (c *Conn) write(t int, buf []byte) error {
	c.ws.SetWriteDeadline(time.Now().Add(writeWaitTime))
	return c.ws.WriteMessage(t, buf)
}

func (c *Conn) Closer(err error) error {
	if c.isClosed {
		return nil
	}
	code := websocket.CloseNormalClosure
	msg := websocket.FormatCloseMessage(code, fmt.Sprint(err))
	c.leave <- msg
	c.isClosed = true
	close(c.leave)
	close(c.output)
	return nil
}

func (c *Conn) Dial(url string, h http.Header) (*Conn, *http.Response, error) {
	var dialer = &websocket.Dialer{}
	ws, r, err := dialer.Dial(url, h)
	if err != nil {
		return nil, r, err
	}
	c.ws = ws
	return c, r, nil
}

func (c *Conn) Error() error {
	return c.err
}

func (c *Conn) Initialize() (chan []byte, error) {
	c.output = make(chan []byte, c.backlog)
	return c.output, nil
}

func (c *Conn) Receiver(r io.ReadCloser) error {
	defer func() { c.ws.Close(); r.Close() }()
	c.ws.SetReadDeadline(time.Now().Add(c.pingTimeout))
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(c.pingTimeout))
		return nil
	})

	for {
		msgt, msg, err := c.ws.ReadMessage()
		switch {
		case err == io.EOF:
			return nil
		case err != nil:
			c.err = ErrReceiverHasGoneAway
			return err
		case msgt == websocket.TextMessage:
			if _, err := r.Read(msg); err != nil {
				c.err = err
				return err
			}
		}
	}
}

func (c *Conn) RemoteAddr() net.Addr {
	return c.ws.RemoteAddr()
}

func (c *Conn) Sender() {
	pingInterval := (c.pingTimeout * 9) / 10
	pinger := time.NewTicker(pingInterval)
	go func() {
		defer pinger.Stop()
		for {
			select {
			case msg := <-c.leave:
				c.write(websocket.CloseMessage, msg)
				return
			case msg := <-c.output:
				if err := c.send(msg); err != nil {
					c.ws.Close()
				}
			case <-pinger.C:
				if err := c.ping(); err != nil {
					c.ws.Close()
				}
			}
		}
	}()
}

func (c *Conn) Upgrade(w http.ResponseWriter, r *http.Request, h http.Header) error {
	ws, err := upgrader.Upgrade(w, r, h)
	if err != nil {
		return err
	}
	c.ws = ws
	return nil
}
