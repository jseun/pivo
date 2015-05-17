package ws

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/jseun/gopivo"
	"github.com/gorilla/websocket"
)

const (
	pongWaitTime  = 60 * time.Second
	pingInterval  = (pongWaitTime * 9) / 10
	writeWaitTime = 10 * time.Second
)

var NormalClosure = websocket.CloseNormalClosure
var InternalServerError = websocket.CloseInternalServerErr

var upgrader = &websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type conn struct {
	err  chan error
	lock *sync.Mutex
	ws   *websocket.Conn
}

func (c *conn) close(code int) (err error) {
	msg := websocket.FormatCloseMessage(code, "")
	err = c.write(websocket.CloseMessage, msg)
	return
}

func (c *conn) ping() error {
	return c.write(websocket.PingMessage, []byte{})
}

func (c *conn) send(buf *bytes.Buffer) (err error) {
	c.ws.SetWriteDeadline(time.Now().Add(writeWaitTime))
	w, err := c.ws.NextWriter(websocket.TextMessage)
	if err != nil {
		return
	}
	_, err = buf.WriteTo(w)
	return
}

func (c *conn) write(t int, buf []byte) error {
	c.ws.SetWriteDeadline(time.Now().Add(writeWaitTime))
	return c.ws.WriteMessage(t, buf)
}

func (c *conn) Closer() error {
	return nil
}

func (c *conn) Reader(r gopivo.Decoder) error {
	defer c.ws.Close()
	c.ws.SetReadDeadline(time.Now().Add(pongWaitTime))
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(pongWaitTime))
		return nil
	})

	for {
		msgt, msg, err := c.ws.ReadMessage()
		switch {
		case err == io.EOF:
			return nil
		case err != nil:
			return gopivo.ErrReaderHasGoneAway
		case msgt == websocket.TextMessage:
			if err := r.Decode(msg); err != nil {
				return err
			}
		default:
			return gopivo.ErrReaderViolation
		}
	}
}

func (c *conn) Writer(init []byte, every time.Duration) chan []byte {
	buf := bytes.NewBuffer(init)
	input := make(chan []byte)
	pinger := time.NewTicker(pingInterval)
	sender := time.NewTicker(every)
	go func() {
		defer func() {
			pinger.Stop()
			sender.Stop()
			c.ws.Close()
		}()
		for {
			select {
			case msg, ok := <-input:
				if !ok {
					c.close(NormalClosure)
					return
				}
				_, err := buf.Write(msg)
				if err != nil {
					c.close(InternalServerError)
					return
				}
			case <-pinger.C:
				if err := c.ping(); err != nil {
					return
				}
			case <-sender.C:
				if err := c.send(buf); err != nil {
					return
				}
			}
		}
	}()

	return input
}

func NewConn(w http.ResponseWriter, r *http.Request, h http.Header) (*conn, error) {
	ws, err := upgrader.Upgrade(w, r, h)
	if err != nil {
		return nil, err
	}
	c := &conn{err: make(chan error), lock: &sync.Mutex{}, ws: ws}
	return c, nil
}
