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

const (
	defaultBacklogSize = 64
	defaultPingTimeout = 60 * time.Second

	writeWaitTime = 10 * time.Second
)

var upgrader = &websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Conn struct {
	backlog int
	port    chan []byte

	pingTimeout time.Duration

	ws *websocket.Conn
}

func NewConn() *Conn {
	return &Conn{
		backlog:     defaultBacklogSize,
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

func (c *Conn) Close(err error) error {
	code := websocket.CloseNormalClosure
	msg := websocket.FormatCloseMessage(code, fmt.Sprint(err))
	wait := time.Now().Add(writeWaitTime)
	return c.ws.WriteControl(websocket.CloseMessage, msg, wait)
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

func (c *Conn) Receiver(rc pivo.OnReadCloser) error {
	defer c.ws.Close()
	c.ws.SetReadDeadline(time.Now().Add(c.pingTimeout))
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(c.pingTimeout))
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

func (c *Conn) RemoteAddr() net.Addr {
	return c.ws.RemoteAddr()
}

func (c *Conn) Sender() chan []byte {
	c.port = make(chan []byte, c.backlog)
	pingInterval := (c.pingTimeout * 9) / 10
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

func (c *Conn) Upgrade(w http.ResponseWriter, r *http.Request, h http.Header) error {
	ws, err := upgrader.Upgrade(w, r, h)
	if err != nil {
		return err
	}
	c.ws = ws
	return nil
}
