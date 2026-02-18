package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

var adminList = make(map[string]bool)
var bannedIP = make(map[string]bool)
var mu sync.Mutex
var ShareFilesNum int

func Check_IP(ip string) bool {
	mu.Lock()
	defer mu.Unlock()
	return bannedIP[ip]
}

func getNowIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()

	Now := conn.LocalAddr().(*net.UDPAddr)
	return Now.IP.String()
}

func startFilesServer() (int, error) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	go func() {
		err := http.Serve(listener, http.FileServer(http.Dir(".")))
		if err != nil {
			fmt.Printf("\n[Error] 文件服务启动失败: %v\n> ", err)
		}
	}()
	return port, nil
}

func main() {
	if Check_IP(getNowIP()) {
		fmt.Printf("bad ip\n")
		return
	}
	fmt.Print("请输入用户名: ")
	scanner := bufio.NewScanner(os.Stdin)
	var name string

	for {
		if scanner.Scan() {
			name = strings.TrimSpace(scanner.Text())
		}
		if name != "" {
			break
		}
		fmt.Println("名字不能为空！！给老子重写！！")
	}

	const (
		colorGreen = "\033[32m"
		colorReset = "\033[0m"
		colorRed   = "\033[31m"
		colorBlue  = "\033[34m"
	)

	if name == "air" {
		adminList[name] = true
		fmt.Printf("%sGet admin success%s\n", colorBlue, colorReset)
	}

	pan, err := net.ListenPacket("udp4", ":1145")
	if err != nil {
		fmt.Printf("监听失败: %v\n", err)
		return
	}
	defer pan.Close()

	addr, _ := net.ResolveUDPAddr("udp4", "255.255.255.255:1145")

	welcome := fmt.Sprintf("System: 📢 %s%s%s 进入了聊天室...", colorGreen, name, colorReset)
	pan.WriteTo([]byte(welcome), addr)

	fmt.Printf("System：你的名字是 %s%s%s\n> ", colorGreen, name, colorReset)

	// 接收广播的协程
	go func() {
		for {
			buf := make([]byte, 1024)
			n, remoteAddr, err := pan.ReadFrom(buf)
			if err != nil {
				continue
			}

			host, _, _ := net.SplitHostPort(remoteAddr.String())
			rawMsg := string(buf[:n])

			if strings.HasPrefix(rawMsg, "admin:") {
				parts := strings.Split(rawMsg, ":")
				if len(parts) < 4 {
					continue
				}

				cmdType := strings.ToUpper(parts[1])
				value := parts[2]
				fromName := parts[3]
				//防止打印 2 次
				if fromName == name {
					continue
				}
				mu.Lock()
				if adminList[fromName] {
					if cmdType == "BAN" {
						bannedIP[value] = true
						fmt.Printf("\r\033[2K%s[System] 管理员 %s 封禁了 IP: %s%s\n> ", colorRed, fromName, value, colorReset)
					} else if cmdType == "ADD_ADMIN" {
						adminList[value] = true
						fmt.Printf("\r\033[2K%s[System] %s 被添加为管理员%s\n> ", colorBlue, value, colorReset)
					} else if cmdType == "UNBAN" {
						bannedIP[value] = false
						fmt.Printf("\r\033[2K%s[System] 管理员 %s 解封了 IP: %s%s\n> ", colorBlue, fromName, value, colorReset)
					} else if cmdType == "DEL_ADMIN" {
						adminList[value] = false
						fmt.Printf("\r\033[2K%s[System] %s 变为了普通用户%s\n> ", colorBlue, value, colorReset)
					}
				}
				mu.Unlock()
				continue
			}

			// 忽略自己发送的普通聊天消息
			if host == getNowIP() {
				continue
			}

			fmt.Printf("\r\033[2K%s\n> ", rawMsg)
		}
	}()

	// 发送消息的主循环
	for {
		if scanner.Scan() {
			text := scanner.Text()
			if strings.TrimSpace(text) == "" {
				fmt.Print("\r\033[1A\033[2K> ")
				continue
			}

			// 1. 封禁 & 解封指令
			if strings.HasPrefix(text, "/ban ") {
				if !adminList[name] {
					fmt.Printf("\033[1A\033[2K\r%s[Error] 你没有权限执行封禁！%s\n> ", colorRed, colorReset)
					continue
				}
				targetIP := strings.TrimPrefix(text, "/ban ")

				// 本地立刻执行
				mu.Lock()
				bannedIP[targetIP] = true
				mu.Unlock()
				fmt.Print("\033[1A\033[2K\r")
				fmt.Printf("%s[System] 你封禁了 IP: %s%s\n> ", colorRed, targetIP, colorReset)

				cmd := fmt.Sprintf("admin:BAN:%s:%s", targetIP, name)
				pan.WriteTo([]byte(cmd), addr)
				continue
			}

			if strings.HasPrefix(text, "/unban ") {
				if !adminList[name] {
					fmt.Printf("\033[1A\033[2K\r%s[Error] 你没有权限执行解封！%s\n> ", colorRed, colorReset)
					continue
				}
				targetIP := strings.TrimPrefix(text, "/unban ")

				mu.Lock()
				bannedIP[targetIP] = false
				mu.Unlock()
				fmt.Print("\033[1A\033[2K\r")
				fmt.Printf("%s[System] 你解封了 IP: %s%s\n> ", colorBlue, targetIP, colorReset)

				cmd := fmt.Sprintf("admin:UNBAN:%s:%s", targetIP, name)
				pan.WriteTo([]byte(cmd), addr)
				continue
			}

			// 2. 添加/删除管理员指令
			if strings.HasPrefix(text, "/op ") {
				if !adminList[name] {
					fmt.Printf("\033[1A\033[2K\r%s[Error] 你没有权限添加管理员！%s\n> ", colorRed, colorReset)
					continue
				}
				targetName := strings.TrimPrefix(text, "/op ")

				mu.Lock()
				adminList[targetName] = true
				mu.Unlock()
				fmt.Print("\033[1A\033[2K\r")
				fmt.Printf("%s[System] 你将 %s 设为管理员%s\n> ", colorBlue, targetName, colorReset)

				cmd := fmt.Sprintf("admin:ADD_ADMIN:%s:%s", targetName, name)
				pan.WriteTo([]byte(cmd), addr)
				continue
			}

			if strings.HasPrefix(text, "/deop ") {
				if !adminList[name] {
					fmt.Printf("\033[1A\033[2K\r%s[Error] 你没有权限删除管理员！%s\n> ", colorRed, colorReset)
					continue
				}
				targetName := strings.TrimPrefix(text, "/deop ")

				mu.Lock()
				adminList[targetName] = false
				mu.Unlock()
				fmt.Print("\033[1A\033[2K\r")
				fmt.Printf("%s[System] 你取消了 %s 的管理员权限%s\n> ", colorBlue, targetName, colorReset)

				cmd := fmt.Sprintf("admin:DEL_ADMIN:%s:%s", targetName, name)
				pan.WriteTo([]byte(cmd), addr)
				continue
			}

			// 3. 分享文件
			if strings.HasPrefix(text, "/send") {
				if ShareFilesNum == 0 {
					port, err := startFilesServer()
					if err != nil {
						fmt.Printf("%s[Error] 开启分享失败: %v%s\n> ", colorRed, err, colorReset)
						continue
					}
					ShareFilesNum = port
				}
				myIP := getNowIP()
				shareMsg := fmt.Sprintf("📂 %s%s%s 分享了代码仓库: http://%s:%d", colorBlue, name, colorReset, myIP, ShareFilesNum)

				fmt.Print("\033[1A\033[2K\r")
				pan.WriteTo([]byte(shareMsg), addr)
				fmt.Printf("%s[System] 分享成功！你的文件服务器运行在: http://%s:%d%s\n> ", colorGreen, myIP, ShareFilesNum, colorReset)
				continue
			}

			// 4. 普通聊天消息
			fullMsg := fmt.Sprintf("[%s%s%s]: %s", colorGreen, name, colorReset, text)
			fmt.Print("\033[1A\033[2K\r")
			fmt.Printf("%s\n> ", fullMsg)
			pan.WriteTo([]byte(fullMsg), addr)
		}
	}
}