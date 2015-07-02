package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"gopkg.in/pivo.v2/ws"
)

var (
	host  = flag.String("h", "localhost", "chat server host")
	port  = flag.String("p", "8000", "chat server port")
)

type reader struct {}

func (r *reader) OnClose(err error) error {
	fmt.Println()
	fmt.Println("Connection to server has terminated")
	os.Exit(0)
	return nil
}

func (r *reader) OnBinaryRead(buf []byte) error {
	return nil
}

func (r *reader) OnTextRead(text string) error {
	fmt.Println()
	fmt.Println(text)
	fmt.Print("> ")
	return nil
}

func shutdown(c *ws.Conn) {
	c.Close(nil)
	fmt.Println()
	os.Exit(0)
}

func waitForUserInput(conn *ws.Conn) {
	reader := bufio.NewReader(os.Stdin)
	for {
		text, _ := reader.ReadString('\n')
		// Remove trailing newline
		msg := []byte(text)[:len(text)-1]
		conn.Send(conn.TextMessage(string(msg)))
	}
}

func main() {
	fmt.Println()
	fmt.Println("Hit CTRL-C to exit.")
	fmt.Println()
	flag.Parse()
	conn := ws.DefaultConn()
	remote := fmt.Sprintf("ws://%s:%s/", *host, *port)
	for {
		sigint := make(chan os.Signal)
		signal.Notify(sigint, os.Interrupt)
		if _, _, err := conn.Dial(remote, nil); err != nil {
			log.Print(err)
		} else {
			go func() { <-sigint; shutdown(conn) }()
			go conn.Receiver(&reader{})
			go conn.Sender()
			waitForUserInput(conn)
		}
		signal.Stop(sigint)
		time.Sleep(time.Second * 5)
	}
}
