package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"gopkg.in/pivo.v2"
)

const banner = `Pivo Hub version %s (c) The Pivo Authors.`

type joiner struct {}

var (
	lHttp = flag.String("http", ":8000", "listen for http on")
)

func (j *joiner) OnJoin(c pivo.Connector, port pivo.Port) (*pivo.Message, error) {
	port <- pivo.TextMessage(fmt.Sprintf(banner, pivo.Version))
	return nil, nil
}

func main() {
	fmt.Printf(banner, pivo.Version)
	fmt.Println()
	flag.Parse()

	// Kick off the hub
	run(&joiner{})

	// Listen for HTTP connections to upgrade
	if err := http.ListenAndServe(*lHttp, nil); err != nil {
		fmt.Println("websocket:", err)
		os.Exit(1)
	}
}
