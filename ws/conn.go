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

var ErrWSClientHasGoneAway = errors.New("websocket has gone away")

var upgrader = &websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type conn struct {
	backlog int
	output  chan []byte

	pingTimeout time.Duration

	err      error
	isClosed bool
	leave    chan []byte
	ws       *websocket.Conn
}

func (c *conn) ping() error {
	return c.write(websocket.PingMessage, []byte{})
}

func (c *conn) send(buf []byte) (err error) {
	return c.write(websocket.TextMessage, buf)
}

func (c *conn) write(t int, buf []byte) error {
	c.ws.SetWriteDeadline(time.Now().Add(writeWaitTime))
	return c.ws.WriteMessage(t, buf)
}

func (c *conn) Closer(err error) error {
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

func (c *conn) Error() error {
	return c.err
}

func (c *conn) Initialize() (chan []byte, error) {
	c.output = make(chan []byte, c.backlog)
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(c.pingTimeout))
		return nil
	})
	return c.output, nil
}

func (c *conn) Receiver(r io.ReadCloser) error {
	defer func() { c.ws.Close(); r.Close() }()
	c.ws.SetReadDeadline(time.Now().Add(c.pingTimeout))
	for {
		msgt, msg, err := c.ws.ReadMessage()
		switch {
		case err == io.EOF:
			return nil
		case err != nil:
			c.err = ErrWSClientHasGoneAway
			return err
		case msgt == websocket.TextMessage:
			if _, err := r.Read(msg); err != nil {
				c.err = err
				return err
			}
		}
	}
}

func (c *conn) RemoteAddr() net.Addr {
	return c.ws.RemoteAddr()
}

func (c *conn) Sender() {
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

func NewConn(w http.ResponseWriter, r *http.Request, h http.Header) (*conn, error) {
	ws, err := upgrader.Upgrade(w, r, h)
	if err != nil {
		return nil, err
	}
	c := &conn{
		backlog:     defaultBacklogSize,
		isClosed:    false,
		leave:       make(chan []byte),
		pingTimeout: defaultPingTimeout,
		ws:          ws,
	}
	return c, nil
}
