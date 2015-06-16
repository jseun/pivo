package main

import (
	"errors"
	"fmt"
	"time"

	"gopkg.in/pivo.v2"
)

type Connector struct {
	pub pivo.Port
}

type Hub struct {
	hub *pivo.Hub
	sub chan pivo.Connector
}

// Error is transmited when the hub is closing
var ErrHubIsClosing = errors.New("hub is going down")

// Error is thrown when the hub receives invalid data
var ErrProtocolViolation = errors.New("protocol violation")

// Define a hub with custom settings
var echo = &Hub{
	hub: &pivo.Hub{
		JoinLimitRateInterval: time.Second,
		JoinLimitRateBurst: 8,
		JoinMaxQueueSize: 16,
	},
	sub: make(chan pivo.Connector),
}

func (c *Connector) OnClose(why error) error {
	return nil
}

func (c *Connector) OnBinaryRead(data []byte) error {
	// Binary transfer is not allowed here
	return ErrProtocolViolation
}

func (c *Connector) OnTextRead(text string) error {
	// Echo back the message to all connectors,
	// including this one.
	c.pub <- pivo.TextMessage(text)
	return nil
}

func run(j pivo.OnJoiner) {
	echo.hub.Joiner = j
	echo.hub.Start()
	go func() {
		bc := echo.hub.NewBroadcast()
		pub := make(pivo.Port)
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
				err := echo.hub.Join(c, conn)
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
