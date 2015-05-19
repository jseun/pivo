package gopivo

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const defaultJoinLimitRatePerSecond = 64
const defaultJoinLimitRateBurst = 32
const defaultJoinMaxQueueSize = 256

var (
	ErrJoinQueueIsFull  = errors.New("join queue is full")
	ErrNoSuchConnector  = errors.New("no such connector")
	ErrPortBufferIsFull = errors.New("port buffer is full")
)

type Connector interface {
	Error() error
	Initialize() (chan []byte, error)
	RemoteAddr() net.Addr

	Closer(error) error
	Receiver(io.ReadCloser) error
	Sender()
}

type Welcomer interface {
	Welcome() ([]byte, error)
}

type Hub struct {
	Name     string
	lock     *sync.Mutex
	ports    Port
	queue    chan chan bool
	throttle chan time.Time
}

type Port map[Connector]chan []byte

func NewHub(name string) *Hub {
	h := &Hub{
		Name:     name,
		lock:     &sync.Mutex{},
		ports:    make(Port),
		queue:    make(chan chan bool, defaultJoinMaxQueueSize),
		throttle: make(chan time.Time, defaultJoinLimitRateBurst),
	}
	go h.run()
	return h
}

func (h Hub) run() {
	go h.ticker(defaultJoinLimitRatePerSecond)
	for waiter := range h.queue {
		<-h.throttle
		waiter <- true
	}
}

func (h Hub) ticker(rate time.Duration) {
	for ns := range time.Tick(time.Second / rate) {
		h.throttle <- ns
	}
}

func (h Hub) waitQueue() error {
	waiter := make(chan bool)
	defer close(waiter)

	select {
	case h.queue <- waiter:
	default:
		return ErrJoinQueueIsFull
	}
	<-waiter
	return nil
}

func (h Hub) Broadcast() chan []byte {
	messages := make(chan []byte)
	go func() {
		defer close(messages)
		for msg := range messages {
			h.lock.Lock()
			for c, port := range h.ports {
				select {
				case port <- msg:
				default:
					err := ErrPortBufferIsFull
					go h.Leave(c, err)
				}
			}
			h.lock.Unlock()
		}
	}()
	return messages
}

func (h Hub) Join(c Connector, r io.ReadCloser, w Welcomer) error {
	if err := h.waitQueue(); err != nil {
		c.Closer(err)
		return err
	}

	port, err := c.Initialize()
	if err != nil {
		c.Closer(err)
		return err
	}

	go c.Sender()
	if w != nil {
		msg, err := w.Welcome()
		if err != nil {
			c.Closer(err)
			return err
		}
		port <- msg
	}

	h.lock.Lock()
	h.ports[c] = port
	h.lock.Unlock()
	go func() { h.Leave(c, c.Receiver(r)) }()
	return nil
}

func (h Hub) Leave(c Connector, reason error) error {
	h.lock.Lock()
	defer h.lock.Unlock()
	if _, ok := h.ports[c]; ok {
		delete(h.ports, c)
		return c.Closer(reason)
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
