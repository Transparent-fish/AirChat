package main

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	adminList     = make(map[string]bool)
	bannedIP      = make(map[string]bool)
	mu            sync.Mutex
	ShareFilesNum int
	// 生成本客户端唯一ID，用于彻底过滤回环消息
	selfNodeID = fmt.Sprintf("%x", md5.Sum([]byte(time.Now().String()+os.Getenv("COMPUTERNAME"))))[:8]
)

const (
	colorGreen = "\033[32m"
	colorReset = "\033[0m"
	colorRed   = "\033[31m"
	colorBlue  = "\033[34m"
)

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
		_ = http.Serve(listener, http.FileServer(http.Dir(".")))
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
	for scanner.Scan() {
		name = strings.TrimSpace(scanner.Text())
		if name != "" {
			break
		}
		fmt.Println("名字不能为空！！给老子重写！！")
	}

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

	welcome := fmt.Sprintf("SYS:%s:📢 %s%s%s 进入了聊天室...", selfNodeID, colorGreen, name, colorReset)
	pan.WriteTo([]byte(welcome), addr)

	fmt.Printf("System：你的名字是 %s%s%s (ID: %s)\n> ", colorGreen, name, colorReset, selfNodeID)

	// 接收协程
	go func() {
		for {
			buf := make([]byte, 1024)
			n, _, err := pan.ReadFrom(buf)
			if err != nil {
				continue
			}

			rawMsg := string(buf[:n])
			parts := strings.SplitN(rawMsg, ":", 3) // [TYPE, ID, CONTENT]
			if len(parts) < 3 {
				continue
			}

			mType, mID, mContent := parts[0], parts[1], parts[2]

			// 如果是自己发出的包不打
			if mID == selfNodeID {
				continue
			}

			switch mType {
			case "ADMIN":
				// CMD:VALUE:FROM
				cp := strings.Split(mContent, ":")
				if len(cp) < 3 {
					continue
				}
				cmdType, val, from := cp[0], cp[1], cp[2]

				mu.Lock()
				if adminList[from] {
					switch cmdType {
					case "BAN":
						bannedIP[val] = true
						fmt.Printf("\r\033[2K%s[System] 管理员 %s 封禁了 IP: %s%s\n> ", colorRed, from, val, colorReset)
					case "UNBAN":
						bannedIP[val] = false
						fmt.Printf("\r\033[2K%s[System] 管理员 %s 解封了 IP: %s%s\n> ", colorBlue, from, val, colorReset)
					case "ADD_ADMIN":
						adminList[val] = true
						fmt.Printf("\r\033[2K%s[System] %s 被添加为管理员%s\n> ", colorBlue, val, colorReset)
					case "DEL_ADMIN":
						adminList[val] = false
						fmt.Printf("\r\033[2K%s[System] %s 变为了普通用户%s\n> ", colorBlue, val, colorReset)
					}
				}
				mu.Unlock()

			case "MSG", "SYS":
				// 直接打印接收到的内容
				fmt.Printf("\r\033[2K%s\n> ", mContent)
			}
		}
	}()

	// 发送主循环
	for scanner.Scan() {
		text := scanner.Text()
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			fmt.Print("\r\033[1A\033[2K> ")
			continue
		}

		// 管理指令处理函数
		handleAdminCmd := func(cmd, value string) {
			if !adminList[name] {
				fmt.Printf("\r\033[1A\033[2K%s[Error] 你没有权限！%s\n> ", colorRed, colorReset)
				return
			}
			// 本地执行逻辑
			mu.Lock()
			if cmd == "BAN" { bannedIP[value] = true }
			if cmd == "UNBAN" { bannedIP[value] = false }
			if cmd == "ADD_ADMIN" { adminList[value] = true }
			if cmd == "DEL_ADMIN" { adminList[value] = false }
			mu.Unlock()

			// 广播指令: ADMIN:ID:CMD:VALUE:NAME
			broadcast := fmt.Sprintf("ADMIN:%s:%s:%s:%s", selfNodeID, cmd, value, name)
			pan.WriteTo([]byte(broadcast), addr)
			// 本地回显
			fmt.Printf("\r\033[1A\033[2K%s[System] 指令执行成功: %s %s%s\n> ", colorBlue, cmd, value, colorReset)
		}

		// 指令解析
		if strings.HasPrefix(trimmed, "/") {
			args := strings.SplitN(trimmed, " ", 2)
			cmd := args[0]
			var val string
			if len(args) > 1 { val = args[1] }

			switch cmd {
			case "/ban": handleAdminCmd("BAN", val)
			case "/unban": handleAdminCmd("UNBAN", val)
			case "/op": handleAdminCmd("ADD_ADMIN", val)
			case "/deop": handleAdminCmd("DEL_ADMIN", val)
			case "/send":
				if ShareFilesNum == 0 {
					port, _ := startFilesServer()
					ShareFilesNum = port
				}
				myIP := getNowIP()
				content := fmt.Sprintf("📂 %s%s%s 分享了代码仓库: http://%s:%d", colorBlue, name, colorReset, myIP, ShareFilesNum)
				pan.WriteTo([]byte(fmt.Sprintf("MSG:%s:%s", selfNodeID, content)), addr)
				fmt.Printf("\r\033[1A\033[2K%s[System] 分享中: http://%s:%d%s\n> ", colorGreen, myIP, ShareFilesNum, colorReset)
			default:
				fmt.Printf("\r\033[1A\033[2K%s[System] 未知指令%s\n> ", colorRed, colorReset)
			}
			continue
		}

		// 普通消息
		fullMsg := fmt.Sprintf("[%s%s%s]: %s", colorGreen, name, colorReset, text)
		// 1. 先在本地打印
		fmt.Printf("\033[1A\033[2K\r%s\n> ", fullMsg)
		// 2. 发送广播 (MSG:ID:CONTENT)
		pan.WriteTo([]byte(fmt.Sprintf("MSG:%s:%s", selfNodeID, fullMsg)), addr)
	}
}