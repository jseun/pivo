package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
)

const banner = `GoPivo Echo Hub version 0.9 (c) The GoPivo Authors.`

type welcomer struct {}

var (
	lHttp = flag.String("http", ":8000", "listen for http on")
)

func (w *welcomer) Welcome() ([]byte, error) {
	return []byte(banner), nil
}

func main() {
	fmt.Println(banner)
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
