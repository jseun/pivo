package main

import (
	"log"
	"net/http"

	"gopkg.in/pivo.v2/ws"
)

const websocketChatUri = `/`

type websocket struct {
	conn *ws.Conn
}

func (ws *websocket) OnClose(why error) error {
	chat.hub.Leave(ws.conn)
	return nil
}

func (ws *websocket) OnBinaryRead(data []byte) error {
	// Binary transfer is not allowed here
	return ErrProtocolViolation
}

func (ws *websocket) OnTextRead(text string) error {
	chat.pub <- ws.conn.TextMessage(formatMessage(ws.conn, text))
	return nil
}

func (chat *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn := ws.DefaultConn()
	if err := conn.Upgrade(w, r, nil); err != nil {
		log.Printf("websocket: %s: failed to upgrade: %s",
			r.RemoteAddr, err)
		return
	}
	go conn.Sender()
	go conn.Receiver(&websocket{conn})
	chat.sub <- conn
}

func init() {
	http.Handle(websocketChatUri, chat)
}
