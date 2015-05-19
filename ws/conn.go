package ws

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultBacklogSize = 1024
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

type conn struct {
	backlog int
	output  chan []byte

	pingTimeout time.Duration

	ws *websocket.Conn
}

func (c *conn) Closer(err error) error {
	defer c.ws.Close()
	code := websocket.CloseNormalClosure
	msg := websocket.FormatCloseMessage(code, fmt.Sprint(err))
	return c.write(websocket.CloseMessage, msg)
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

func (c *conn) Initialize() (chan []byte, error) {
	c.output = make(chan []byte, c.backlog)
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(c.pingTimeout))
		return nil
	})
	return c.output, nil
}

func (c *conn) Receiver(r io.Reader) error {
	defer c.ws.Close()
	c.ws.SetReadDeadline(time.Now().Add(c.pingTimeout))
	for {
		msgt, msg, err := c.ws.ReadMessage()
		switch {
		case err == io.EOF:
			return nil
		case err != nil:
			return err
		case msgt == websocket.TextMessage:
			if _, err := r.Read(msg); err != nil {
				return err
			}
		}
	}
}

func (c *conn) Sender() {
	pingInterval := (c.pingTimeout * 9) / 10
	pinger := time.NewTicker(pingInterval)
	go func() {
		defer func() { pinger.Stop(); c.ws.Close() }()
		for {
			select {
			case msg, ok := <-c.output:
				if !ok {
					c.Closer(nil)
					return
				}
				if err := c.send(msg); err != nil {
					return
				}
			case <-pinger.C:
				if err := c.ping(); err != nil {
					return
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
		pingTimeout: defaultPingTimeout,
		ws:          ws,
	}
	return c, nil
}
