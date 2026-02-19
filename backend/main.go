package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	//init
	r := gin.Default()
	hub := newHub()
	go hub.run()

	r.GET("/ws", func(c *gin.Context) {
		//http -> WebSocket
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			} else {
				log.Println("💡 用户正常离开了聊天室")
			}
		}

		//创建 Client
		client := &Client{
			hub:  hub,
			conn: conn,
			send: make(chan Message, 256),
		}

		client.hub.register <- client

		go client.writPump()
		go client.readPump()
	})

	//启动！！！！！
	fmt.Println("------------------------------")
	fmt.Println("🚀 聊天室后端已成功启动！")
	fmt.Println("📍 监听端口: :8080")
	fmt.Println("🔗 WebSocket 入口: ws://127.0.0.1:8080/ws")
	fmt.Println("------------------------------")

	r.Run(":8080")
}
