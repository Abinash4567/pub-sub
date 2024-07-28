package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/signal"

	"github.com/gorilla/websocket"
)

type Message struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

func main() {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	u := url.URL{Scheme: "ws", Host: "localhost:8000", Path: "/sender"}
	log.Printf("Connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}
			log.Printf("Received: %s", message)
		}
	}()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		select {
		case <-done:
			return
		case <-interrupt:
			log.Println("Interrupt received, closing connection...")
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
			}
			select {
			case <-done:
			}
			return
		default:
			fmt.Print("Enter message: ")
			if scanner.Scan() {
				msg := Message{
					Type:    "text",
					Content: scanner.Text(),
				}
				jsonMsg, err := json.Marshal(msg)
				if err != nil {
					log.Println("json marshal:", err)
					continue
				}
				err = c.WriteMessage(websocket.TextMessage, jsonMsg)
				if err != nil {
					log.Println("write:", err)
					return
				}
			}
		}
	}
}