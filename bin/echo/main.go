package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"gopkg.in/pivo.v1"
)

const banner = `Pivo Hub version %s (c) The Pivo Authors.`

type welcomer struct {}

var (
	lHttp = flag.String("http", ":8000", "listen for http on")
)

func (w *welcomer) Welcome() ([]byte, error) {
	return []byte(fmt.Sprintf(banner, pivo.Version)), nil
}

func main() {
	fmt.Printf(banner, pivo.Version)
	fmt.Println()
	flag.Parse()

	// Kick off the hub
	run(&welcomer{})

	// Listen for HTTP connections to upgrade
	if err := http.ListenAndServe(*lHttp, nil); err != nil {
		fmt.Println("websocket:", err)
		os.Exit(1)
	}
}
