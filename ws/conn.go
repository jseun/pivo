package ws

import (
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

func (c *conn) Closer() error {
	return nil
}

func (c *conn) Reader(r io.Reader) error {
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
			n, err := r.Read(msg)
			if err != nil || n != len(msg) {
				return err
			} else if n != len(msg) {
				return gopivo.ErrReaderIsWastingData
			}
		default:
			return gopivo.ErrReaderMisunderstood
		}
	}
}

func (c *conn) write(t int, buf []byte) error {
	c.ws.SetWriteDeadline(time.Now().Add(writeWaitTime))
	return c.ws.WriteMessage(t, buf)
}

func (c *conn) Writer(w io.Writer, initial []byte) chan []byte {
	pipe := make(chan []byte)
	ticker := time.NewTicker(pingInterval)
	go func() {
		defer func() { ticker.Stop(); c.ws.Close() }()
		for {
			select {
			case msg, ok := <-pipe:
				if !ok {
					c.close(websocket.CloseNormalClosure)
					return
				}

				err := c.write(websocket.TextMessage, msg)
				if err != nil {
					return
				}

			case <-ticker.C:
				if err := c.ping(); err != nil {
					return
				}
			}
		}
	}()

	return pipe
}

func NewConn(w http.ResponseWriter, r *http.Request, h http.Header) (*conn, error) {
	ws, err := upgrader.Upgrade(w, r, h)
	if err != nil {
		return nil, err
	}
	c := &conn{err: make(chan error), lock: &sync.Mutex{}, ws: ws}
	return c, nil
}
