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
	conn, err := net.Dial("udp", "111.63.65.247")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()

	Now := conn.LocalAddr().(*net.UDPAddr)
	return Now.IP.String()
}

func startFilesServer() (int, error) {
	// 1. 选端口
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	// 2. 获取分配端口
	port := listener.Addr().(*net.TCPAddr).Port
	// 3. 异步启动
	go func() {
		// http.FileServer(http.Dir("./"))
		err := http.Serve(listener, http.FileServer(http.Dir(".")))
		if err != nil {
			fmt.Printf("\n[Error] 文件服务启动失败: %v\n> ", err)
		}
	}()
	return port, nil
}

func main() {
	//如果当前 IP 被封禁
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

	fmt.Printf("System：你的名字是 %s%s%s\n", colorGreen, name, colorReset)

	go func() {
		for {
			buf := make([]byte, 1024)
			n, remoteAddr, err := pan.ReadFrom(buf)
			if err != nil {
				continue
			}

			host, _, _ := net.SplitHostPort(remoteAddr.String())
			if Check_IP(host) {
				continue
			}

			rawMsg := string(buf[:n])

			if strings.HasPrefix(rawMsg, "admin:") {
				parts := strings.Split(rawMsg, ":")
				if len(parts) < 4 {
					continue
				}

				cmdType := strings.ToUpper(parts[1]) // 统一大写
				value := parts[2]
				fromName := parts[3]

				mu.Lock()
				if adminList[fromName] {
					if cmdType == "BAN" {
						bannedIP[value] = true
						fmt.Printf("\r%s[System] 管理员 %s 封禁了 IP: %s%s\n> ", colorRed, fromName, value, colorReset)
					} else if cmdType == "ADD_ADMIN" {
						adminList[value] = true
						fmt.Printf("\r%s[System] %s 被添加为管理员%s\n> ", colorBlue, value, colorReset)
					} else if cmdType == "UNBAN" {
						bannedIP[value] = false
						fmt.Printf("\r%s[System] 管理员 %s 解封了 IP: %s%s\n", colorBlue, fromName, value, colorReset)
					} else if cmdType == "DEL_ADMIN" {
						adminList[value] = false
						fmt.Printf("\r%s[System] %s 变为了普通用户%s\n> ", colorBlue, value, colorReset)
					}
				}
				mu.Unlock()
				continue
			}

			fmt.Printf("\r%s\n> ", rawMsg)
		}
	}()

	for {
		if scanner.Scan() {
			text := scanner.Text()
			if strings.TrimSpace(text) == "" {
				continue
			}

			// 1. 封禁 & 解封指令
			if strings.HasPrefix(text, "/ban ") { //封禁
				if !adminList[name] {
					fmt.Printf("%s[Error] 你没有权限执行封禁！%s\n> ", colorRed, colorReset)
					continue
				}
				targetIP := strings.TrimPrefix(text, "/ban ")
				cmd := fmt.Sprintf("admin:BAN:%s:%s", targetIP, name)
				pan.WriteTo([]byte(cmd), addr)
				continue
			}
			if strings.HasPrefix(text, "/unban") { //解封
				if !adminList[name] {
					fmt.Printf("%s[Error] 你没有权限执行封禁！%s\n> ", colorRed, colorReset)
					continue
				}
				targetIP := strings.TrimPrefix(text, "/unban ")
				cmd := fmt.Sprintf("admin:UNBAN:%s:%s", targetIP, name)
				pan.WriteTo([]byte(cmd), addr)
				continue
			}

			// 2. 添加管理员指令
			if strings.HasPrefix(text, "/op ") { //添加
				if !adminList[name] {
					fmt.Printf("%s[Error] 你没有权限添加管理员！%s\n> ", colorRed, colorReset)
					continue
				}
				targetName := strings.TrimPrefix(text, "/op ")
				cmd := fmt.Sprintf("admin:ADD_ADMIN:%s:%s", targetName, name)
				pan.WriteTo([]byte(cmd), addr)
				continue
			}
			if strings.HasPrefix(text, "/deop ") { //删除
				if !adminList[name] {
					fmt.Printf("%s[Error] 你没有权限添加管理员！%s\n> ", colorRed, colorReset)
					continue
				}
				targetName := strings.TrimPrefix(text, "/deop ")
				cmd := fmt.Sprintf("admin:DEL_ADMIN:%s:%s", targetName, name)
				pan.WriteTo([]byte(cmd), addr)
				continue
			}
			fullMsg := fmt.Sprintf("[%s%s%s]: %s", colorGreen, name, colorReset, text)
			fmt.Print("\033[A\r")
			pan.WriteTo([]byte(fullMsg), addr)

			//3. 分享文件
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
				// 构造 HTTP 链接
				shareMsg := fmt.Sprintf("📂 %s%s%s 分享了代码仓库: http://%s:%d", colorBlue, name, colorReset, myIP, ShareFilesNum)
				// 广播给所有人
				pan.WriteTo([]byte(shareMsg), addr)
				fmt.Printf("%s[System] 分享成功！你的文件服务器运行在: http://%s:%d%s\n> ", colorGreen, myIP, ShareFilesNum, colorReset)
				continue
			}
		}
	}
}
