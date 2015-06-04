package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/jseun/gopivo"
)

type Connector struct {
	pub chan []byte
}

type Hub struct {
	hub *gopivo.Hub
	sub chan gopivo.Connector
}

// Error is transmited when the hub is closing
var ErrHubIsClosing = errors.New("hub is going down")

// Error is thrown when the hub receives invalid data
var ErrProtocolViolation = errors.New("protocol violation")

// Define a hub with custom settings
var echo = &Hub{
	hub: &gopivo.Hub{
		JoinLimitRateInterval: time.Second,
		JoinLimitRateBurst: 8,
		JoinMaxQueueSize: 16,
	},
	sub: make(chan gopivo.Connector),
}

func (c *Connector) OnClose(why error) error {
	return nil
}

func (c *Connector) OnReadBinary(data []byte) error {
	// Binary transfer is not allowed here
	return ErrProtocolViolation
}

func (c *Connector) OnReadText(msg []byte) error {
	// Echo back the message to all connectors,
	// including this one.
	c.pub <-msg
	return nil
}

func run(w gopivo.Welcomer) {
	echo.hub.Start()
	go func() {
		bc := echo.hub.NewBroadcast()
		pub := make(chan []byte)
		defer func() {
			echo.hub.Stop(ErrHubIsClosing)
			bc.Close()
			close(pub)
		}()

		// Forever loop.  Subscribing or publishing.
		for {
			select {

			// Subscribing
			case c, ok := <-echo.sub:
				if !ok {
					return
				}
				conn := &Connector{pub: pub}
				err := echo.hub.Join(c, conn, w)
				if err != nil {
					fmt.Printf("%s: %s",
						c.RemoteAddr(), err)
				}

			// Publishing
			case msg := <-pub:
				bc.C <-msg

			}
		}
	}()
}
