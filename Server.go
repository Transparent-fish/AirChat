package main

import (
	"bufio"
	"fmt"
	"net"
)

var (
	message  = make(chan string)
	entering = make(chan chan string)
	leaving  = make(chan chan string)
)

func startServer() {
	clients := make(map[chan string]bool)

	for {
		select {
		case msg := <-message:
			// 收到新消息
			for cli := range clients {
				cli <- msg
			}
		case cli := <-entering:
			// 有人来了 加入 map
			clients[cli] = true
		case cli := <-leaving:
			// 有人走了 从 map 移除
			delete(clients, cli)
			close(cli)
		}
	}
}

func NewConn(conn net.Conn) {
	defer conn.Close()
	//私人通道
	ch := make(chan string)
	//写道其他人的客户端上
	go func() {
		for i := range ch {
			fmt.Fprintln(conn, i)
		}
	}()
	//加入
	who := conn.RemoteAddr().String()
	message <- "[System]" + who + "进入了聊天室"
	entering <- ch
	//读入
	input := bufio.NewScanner(conn)
	for input.Scan() {
		message <- who + ": " + input.Text()
	}
	//离开
	leaving <- ch
	message <- "[System]" + who + "离开了聊天室"
}

func main() {
	listener, _ := net.Listen("tcp", "127.0.0.1:8000")
	go startServer() //广播中心

	for {
		conn, _ := listener.Accept()
		go NewConn(conn)
	}
}
