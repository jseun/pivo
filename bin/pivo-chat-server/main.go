package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"

	"gopkg.in/pivo.v2"
)

const MainBanner = `Pivo version %s (c) The Pivo Authors.`
const WelcomeBanner = `Welcome %s! Chat server is running Pivo %s`

var (
	lHttp = flag.String("http", ":8000", "listen for http on")
)

func formatMessage(conn pivo.Connector, text string) string {
	return fmt.Sprintf("(%s/%s): %s", conn.Protocol(),
		conn.RemoteAddr().String(), text)
}

func main() {
	fmt.Printf(MainBanner, pivo.Version)
	fmt.Println()
	flag.Parse()

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt)
	go func() { exit := run(); <-c; exit <-true }()

	// Listen for HTTP connections to upgrade
	if err := http.ListenAndServe(*lHttp, nil); err != nil {
		fmt.Println("websocket:", err)
		os.Exit(1)
	}
}
