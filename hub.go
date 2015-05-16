package gopivo

import (
	"log"
	"errors"
	"io"
	"time"
)
const limitRatePerSecond = 1
const limitRateBurst = 10
const joinMaxQueueSize = 1024

var (
//	ErrHubIsShuttingDown   = errors.New("hub is shutting down")
//	ErrHubKillWentBad      = errors.New("error while shutting down")
//	ErrNoSuchConnector     = errors.New("no such connector")
	ErrReaderHasGoneAway   = errors.New("reader has gone away")
	ErrReaderIsWastingData = errors.New("reader performed a short read")
	ErrReaderMisunderstood = errors.New("reader received unknown data")
)

type Connector interface {
	Closer() error
	Reader(io.Reader) error
	Writer(io.Writer, []byte) chan []byte
}

type Hub struct {
	Name      string
	broadcast chan []byte
	join      chan Pair
	leave     chan Connector
	ports     Port
	throttle  chan time.Time
}

type Pair struct {
	c Connector
	w chan []byte
}

type Port map[Connector]chan []byte

func NewHub(name string) *Hub {
	h := &Hub{
		Name:      name,
		broadcast: make(chan []byte),
		join:      make(chan Pair, joinMaxQueueSize),
		leave:     make(chan Connector),
		ports:     make(Port),
		throttle:  make(chan time.Time, limitRateBurst),
	}

	go func() {
		for ns := range time.Tick(time.Second / limitRatePerSecond) {
			h.throttle <- ns
			log.Print(ns)
		}
	}()

	go func() {
		for {
			select {
			case <-h.throttle:
				for {
					select {
					case p := <-h.join:
						log.Print("join")
						h.ports[p.c] = p.w
					default:
					}
				}
			case m := <-h.broadcast:
				for _, port := range h.ports {
					port <- m
				}
			case c := <-h.leave:
				if port, ok := h.ports[c]; ok {
					c.Closer()
					delete(h.ports, c)
					close(port)
				}
			}
		}
	}()

	return h
}

func (h Hub) Broadcast() chan []byte {
	messages := make(chan []byte)
	go func() {
		defer close(messages)
		for msg := range messages {
			h.broadcast <- msg
		}
	}()
	return messages
}

func (h Hub) Join(c Connector, rw io.ReadWriter, buf []byte) error {
	defer func() { h.leave <- c }()
	h.join <- Pair{c, c.Writer(rw, buf)}
	return c.Reader(rw)
}

/*
func (h Hub) Kill() (error, []error) {
	atomic.AddUint32(&h.state, 1)
	var errors []error
	for conn, _ := range h.ports {
		if err := conn.Closer(); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return ErrHubKillWentBad, errors
	}
	return nil, nil
}
*/
