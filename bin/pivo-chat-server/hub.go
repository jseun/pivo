package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"gopkg.in/pivo.v2"
)

type Hub struct {
	hub *pivo.Hub
	pub chan *pivo.Message
	sub chan pivo.Connector
}

type HubEvent struct {
	hub *Hub
	pivo.OnHubBroadcaster
	pivo.OnHubCloser
	pivo.OnHubJoiner
	pivo.OnHubLeaver
}

// Error is transmited when the hub is closing
var ErrHubIsClosing = errors.New("hub is going down")

// Error is thrown when the hub receives invalid data
var ErrProtocolViolation = errors.New("protocol violation")

// Define a hub with custom settings
var chat = &Hub{
	hub: &pivo.Hub{
		JoinLimitRateInterval: time.Second,
		JoinLimitRateBurst: 8,
		JoinMaxQueueSize: 16,
	},
	pub: make(chan *pivo.Message),
	sub: make(chan pivo.Connector),
}

func (e *HubEvent) OnHubBroadcast(msg *pivo.Message) (pivo.Ports, error) {
	log.Printf("Broadcasting message from %s: %q",
		msg.From.RemoteAddr().String(), msg.Data)
	return nil, nil
}

func (e *HubEvent) OnHubJoin(conn pivo.Connector) error {
	msg := fmt.Sprintf(WelcomeBanner,
		conn.RemoteAddr().String(), pivo.Version)
	conn.Send(pivo.TextMessage(nil, msg))
	log.Printf("Connector from %s has joined the hub",
		conn.RemoteAddr().String())
	return nil
}

func (e *HubEvent) OnHubLeave(conn pivo.Connector) {
	log.Printf("Connector from %s has left the hub",
		conn.RemoteAddr().String())
}

func (e *HubEvent) OnHubClose(ports pivo.Ports) {
	fmt.Println()
	log.Println("Hub is shutting down")
	for c, _ := range ports {
		log.Println("Closing connection from",
			c.RemoteAddr().String())
		c.Close(nil)
	}
	os.Exit(0)
}

func run() chan<- bool {
	exit := make(chan bool)
	go chat.hub.StartAndServe(&HubEvent{hub: chat})
	go func() {
		defer chat.hub.Stop()
		// Forever loop.  Subscribing or publishing.
		for {
			select {

			// Subscribing
			case c := <-chat.sub:
				if err := chat.hub.Join(c); err != nil {
					fmt.Printf("%s: %s",
						c.RemoteAddr(), err)
				}

			// Publishing
			case msg := <-chat.pub:
				chat.hub.Broadcast(msg)
				msg.From.Send(msg)

			// Shutting down
			case <-exit:
				return
			}
		}
	}()
	return exit
}
