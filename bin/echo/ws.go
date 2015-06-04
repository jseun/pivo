package main

import (
	"log"
	"net/http"

	"github.com/jseun/gopivo/ws"
)

const echoWebsocketUri = `/`

func (echo *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn := ws.NewConn()
	if err := conn.Upgrade(w, r, nil); err != nil {
		log.Printf("websocket: %s: failed to upgrade: %s",
			r.RemoteAddr, err)
		return
	}
	echo.sub <-conn
}

func init() {
	http.Handle(echoWebsocketUri, echo)
}