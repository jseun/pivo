package gopivo

import (
	"errors"
	"io"
	"sync"
	"time"
)
const limitRatePerSecond = 1
const limitRateBurst = 10
const joinMaxQueueSize = 1024

var (
	ErrHubIsDown         = errors.New("hub is shutting down")
	ErrJoinQueueIsFull   = errors.New("join queue is full")
	ErrNoSuchConnector   = errors.New("no such connector")
	ErrReaderHasGoneAway = errors.New("reader has gone away")
	ErrReaderShortRead   = errors.New("reader short read")
	ErrReaderViolation   = errors.New("reader violation")
)

type Connector interface {
	Closer() error
	Reader(io.Reader) error
	Writer(io.Writer, []byte) chan []byte
}

type Hub struct {
	Name      string
	broadcast chan []byte
	lock      *sync.Mutex
	ports     Port
	queue     chan chan bool
	throttle  chan time.Time
}

type Port map[Connector]chan []byte

func NewHub(name string) *Hub {
	h := &Hub{
		Name:      name,
		broadcast: make(chan []byte),
		lock:      &sync.Mutex{},
		ports:     make(Port),
		queue:     make(chan chan bool, joinMaxQueueSize),
		throttle:  make(chan time.Time, limitRateBurst),
	}
	go h.run()
	return h
}

func (h Hub) run() {
	go h.ticker(limitRatePerSecond)
	for ok := range h.queue {
		<-h.throttle
		ok <- true
	}
}

func (h Hub) ticker(rate time.Duration) {
	for ns := range time.Tick(time.Second / rate) {
		h.throttle <- ns
	}
}

func (h Hub) Broadcast() chan []byte {
	messages := make(chan []byte)
	go func() {
		defer close(messages)
		for msg := range messages {
			h.lock.Lock()
			for _, port := range h.ports {
				port <- msg
			}
			h.lock.Unlock()
		}
	}()
	return messages
}

func (h Hub) Join(c Connector, rw io.ReadWriter, buf []byte) error {
	ok := make(chan bool)
	select {
	case h.queue <- ok:
	default:
		close(ok)
		c.Closer()
		return ErrJoinQueueIsFull
	}
	<-ok
	close(ok)
	h.lock.Lock()
	h.ports[c] = c.Writer(rw, buf)
	h.lock.Unlock()
	defer h.Leave(c)
	return c.Reader(rw)
}

func (h Hub) Leave(c Connector) error {
	h.lock.Lock()
	defer h.lock.Unlock()
	if port, ok := h.ports[c]; ok {
		c.Closer()
		delete(h.ports, c)
		close(port)
		return nil
	}
	return ErrNoSuchConnector
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
