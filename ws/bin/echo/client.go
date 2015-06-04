package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/jseun/gopivo/ws"
)

var (
	host  = flag.String("h", "localhost", "echo hub host")
	port  = flag.String("p", "8000", "echo hub port")
)

type reader struct {}

func (r *reader) OnClose(err error) error {
	return nil
}

func (r *reader) OnReadBinary(buf []byte) error {
	return nil
}

func (r *reader) OnReadText(buf []byte) error {
	fmt.Println()
	fmt.Println(string(buf))
	fmt.Print("> ")
	return nil
}

func shutdown(c *ws.Conn) {
	c.Close(nil)
	fmt.Println()
	os.Exit(0)
}

func waitForUserInput(port chan []byte) {
	reader := bufio.NewReader(os.Stdin)
	for {
		text, _ := reader.ReadString('\n')
		port <-[]byte(text)[:len(text)-1]
	}
}

func main() {
	fmt.Println()
	fmt.Println("Hit CTRL-C to exit.")
	fmt.Println()
	flag.Parse()
	conn := ws.NewConn()
	remote := fmt.Sprintf("ws://%s:%s/", *host, *port)
	for {
		sigint := make(chan os.Signal)
		signal.Notify(sigint, os.Interrupt)
		if _, _, err := conn.Dial(remote, nil); err != nil {
			log.Print(err)
		} else {
			go func() { <-sigint; shutdown(conn) }()
			port := conn.Sender()
			go func() {
				err := conn.Receiver(&reader{})
				if err != nil {
					log.Print(err)
				}
			}()
			waitForUserInput(port)
		}
		signal.Stop(sigint)
		time.Sleep(time.Second * 5)
	}
}
