package main

import (
	"log"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	hub  *Hub
	conn *websocket.Conn //websocket链接
	send chan Message    //消息
}

// 读发的消息
func (c *Client) readPump() {
	for {
		//读取消息
		_, text, err := c.conn.ReadMessage()
		if err != nil {
			log.Printf("erroe: %v", err)
		}
		//整成 Message
		meg := Message{
			Sender:  c.conn.RemoteAddr().String(),
			Content: string(text),
			Time:    time.Now().Format("15:04"),
			Type:    "user",
		}
		//丢进广播
		c.hub.broadcast <- meg
	}
}

// 打印发的消息
func (c *Client) writPump() {
	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				//hub把通道关了
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteJSON(message); err != nil {
				return
			}
		}
	}
}
