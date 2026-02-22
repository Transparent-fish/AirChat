package main

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

//go:embed dist
var frontendStatic embed.FS

var db *gorm.DB

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var jwtKey = []byte("air_chat_secret_key_12345") // 在实际生产中应使用环境变量

type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// 获取请求的真实IP
func getClientIP(c *gin.Context) string {
	ip := c.ClientIP()
	if ip == "::1" {
		ip = "127.0.0.1"
	}
	host, _, err := net.SplitHostPort(ip)
	if err == nil {
		return host
	}
	return ip
}

// 检查IP是否被封禁
func isIPBanned(ip string) bool {
	if ip == "" {
		return false
	}
	var bans []IPBan
	db.Find(&bans)

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	for _, ban := range bans {
		if ban.IsRange {
			// 支持 CIDR 或 IP-IP 区间模式
			if strings.Contains(ban.IP, "/") {
				_, ipNet, err := net.ParseCIDR(ban.IP)
				if err == nil && ipNet.Contains(parsedIP) {
					return true
				}
			} else if strings.Contains(ban.IP, "-") {
				parts := strings.Split(ban.IP, "-")
				if len(parts) == 2 {
					start := net.ParseIP(strings.TrimSpace(parts[0])).To4()
					end := net.ParseIP(strings.TrimSpace(parts[1])).To4()
					target := parsedIP.To4()

					if start != nil && end != nil && target != nil {
						if bytes.Compare(target, start) >= 0 && bytes.Compare(target, end) <= 0 {
							return true
						}
					}
				}
			}
		} else {
			if ban.IP == ip {
				return true
			}
		}
	}
	return false
}

func main() {
	// 初始化数据库
	var err error
	db, err = gorm.Open(sqlite.Open("air_chat.db"), &gorm.Config{})
	if err != nil {
		log.Fatalf("无法连接数据库: %v", err)
	}

	// 自动迁移
	db.AutoMigrate(&User{}, &Message{}, &IPBan{}, &Config{})

	// 初始化默认管理员和系统管理员密码
	var adminConfig Config
	if err := db.Where("key = ?", "admin_password").First(&adminConfig).Error; err != nil {
		db.Create(&Config{Key: "admin_password", Value: "admin123"})
	}
	var systemConfig Config
	if err := db.Where("key = ?", "system_password").First(&systemConfig).Error; err != nil {
		db.Create(&Config{Key: "system_password", Value: "system123"})
	}

	// 初始化 Gin
	r := gin.Default()

	// 添加 CORS 中间件
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization"}
	r.Use(cors.New(config))

	hub := newHub()
	go hub.run()

	// 注册接口
	r.POST("/api/register", func(c *gin.Context) {
		if isIPBanned(getClientIP(c)) {
			c.JSON(http.StatusForbidden, gin.H{"error": "您的IP已被封禁"})
			return
		}

		var req struct {
			Username string `json:"username" binding:"required"`
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		// 验证用户名规则：字母/数字/下划线，不超过12位
		match, _ := regexp.MatchString("^[a-zA-Z0-9_]{1,12}$", req.Username)
		if !match {
			c.JSON(http.StatusBadRequest, gin.H{"error": "用户名不符合规则（仅限12位以内字母/数字/下划线）"})
			return
		}

		// 检查用户名是否重复
		var existingUser User
		if err := db.Where("username = ?", req.Username).First(&existingUser).Error; err == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "用户名已存在"})
			return
		}

		// 密码加密
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)

		user := User{
			Username: req.Username,
			Password: string(hashedPassword),
			Avatar:   "https://api.dicebear.com/7.x/bottts/svg?seed=" + req.Username,
			Role:     "user",
		}

		if err := db.Create(&user).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "注册失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "注册成功"})
	})

	// 登录接口
	r.POST("/api/login", func(c *gin.Context) {
		if isIPBanned(getClientIP(c)) {
			c.JSON(http.StatusForbidden, gin.H{"error": "您的IP已被封禁"})
			return
		}

		var req struct {
			Username string `json:"username" binding:"required"`
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		var user User
		if err := db.Where("username = ?", req.Username).First(&user).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
			return
		}

		if user.IsBanned {
			c.JSON(http.StatusForbidden, gin.H{"error": "该账号已被封禁"})
			return
		}

		// 生成 JWT
		expirationTime := time.Now().Add(24 * time.Hour)
		claims := &Claims{
			Username: user.Username,
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(expirationTime),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, err := token.SignedString(jwtKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "生成的 Token 失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"token":    tokenString,
			"username": user.Username,
			"avatar":   user.Avatar,
			"role":     user.Role,
		})
	})

	// WebSocket 入口 (需要 Token)
	r.GET("/ws", func(c *gin.Context) {
		if isIPBanned(getClientIP(c)) {
			c.JSON(http.StatusForbidden, gin.H{"error": "您的IP已被封禁"})
			return
		}

		tokenString := c.Query("token")
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未提供 Token"})
			return
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "无效的 Token"})
			return
		}

		// 获取用户信息
		var user User
		if err := db.Where("username = ?", claims.Username).First(&user).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户不存在"})
			return
		}

		// http -> WebSocket
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("upgrade error: %v", err)
			return
		}

		client := &Client{
			hub:        hub,
			conn:       conn,
			send:       make(chan Message, 256),
			Username:   user.Username,
			Avatar:     user.Avatar,
			Role:       user.Role,
			Identifier: conn.RemoteAddr().String(),
		}

		client.hub.register <- client

		go client.writPump()
		go client.readPump()
	})

	// JWT 中间件
	authMiddleware := func(c *gin.Context) {
		tokenString := c.GetHeader("Authorization")
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未提供 Token"})
			c.Abort()
			return
		}

		// 处理 "Bearer <token>"
		if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
			tokenString = tokenString[7:]
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "无效的 Token"})
			c.Abort()
			return
		}

		c.Set("username", claims.Username)
		c.Next()
	}

	// 头像上传接口
	os.MkdirAll("./uploads", os.ModePerm)
	r.Static("/uploads", "./uploads")

	r.POST("/api/upload-avatar", authMiddleware, func(c *gin.Context) {
		file, err := c.FormFile("avatar")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无法获取文件"})
			return
		}

		username := c.MustGet("username").(string)
		filename := fmt.Sprintf("%d_%s", time.Now().Unix(), file.Filename)
		filepath := "./uploads/" + filename

		if err := c.SaveUploadedFile(file, filepath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败"})
			return
		}

		// 更新数据库中的用户头像
		avatarURL := "/uploads/" + filename
		if err := db.Model(&User{}).Where("username = ?", username).Update("avatar", avatarURL).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新数据库失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message": "上传成功",
			"url":     avatarURL,
		})
	})

	// ====== 文件共享路由 ======
	os.MkdirAll("./shared", os.ModePerm)
	r.Static("/shared", "./shared")

	// 上传文件夹（需登录）
	r.POST("/api/upload-folder", authMiddleware, func(c *gin.Context) {
		username := c.MustGet("username").(string)
		folderName := c.PostForm("folderName")
		if folderName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "文件夹名不能为空"})
			return
		}
		// 清理文件夹名防止路径穿越
		folderName = strings.ReplaceAll(folderName, "..", "")
		folderName = strings.Trim(folderName, "/\\")
		destDir := fmt.Sprintf("./shared/%s_%s", username, folderName)
		os.MkdirAll(destDir, os.ModePerm)

		form, err := c.MultipartForm()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无法解析表单"})
			return
		}
		files := form.File["files"]
		paths := form.Value["paths"]
		for i, file := range files {
			relPath := ""
			if i < len(paths) {
				relPath = paths[i]
			} else {
				relPath = file.Filename
			}
			// 只保留相对路径部分（去掉顶层文件夹）
			parts := strings.SplitN(relPath, "/", 2)
			if len(parts) == 2 {
				relPath = parts[1]
			} else {
				relPath = file.Filename
			}
			destPath := destDir + "/" + relPath
			os.MkdirAll(filepath.Dir(destPath), os.ModePerm)
			c.SaveUploadedFile(file, destPath)
		}
		c.JSON(http.StatusOK, gin.H{"message": "上传成功"})
	})

	// 获取当前用户分享的文件夹列表（需登录）
	r.GET("/api/my-folders", authMiddleware, func(c *gin.Context) {
		username := c.MustGet("username").(string)
		prefix := username + "_"
		entries, err := os.ReadDir("./shared")
		if err != nil {
			c.JSON(http.StatusOK, []string{})
			return
		}
		var folders []string
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
				// 返回去掉用户名前缀的原始文件夹名
				folders = append(folders, strings.TrimPrefix(e.Name(), prefix))
			}
		}
		c.JSON(http.StatusOK, folders)
	})

	// 获取所有用户共享的文件夹（公共，需登录）
	r.GET("/api/shared-folders", authMiddleware, func(c *gin.Context) {
		subPath := c.Query("path")
		// 清理路径防止穿越
		subPath = strings.ReplaceAll(subPath, "..", "")
		subPath = strings.Trim(subPath, "/\\")

		baseDir := "./shared"
		targetDir := baseDir
		if subPath != "" {
			targetDir = filepath.Join(baseDir, subPath)
		}

		entries, err := os.ReadDir(targetDir)
		if err != nil {
			c.JSON(http.StatusOK, []gin.H{})
			return
		}

		var result []gin.H
		for _, e := range entries {
			info, _ := e.Info()
			fullRelPath := e.Name()
			if subPath != "" {
				fullRelPath = subPath + "/" + e.Name()
			}

			item := gin.H{
				"name":   e.Name(),
				"path":   fullRelPath,
				"is_dir": e.IsDir(),
				"size":   int64(0),
			}

			if info != nil {
				item["size"] = info.Size()
			}

			// 如果是在顶层目录，尝试解析 owner
			if subPath == "" {
				parts := strings.SplitN(e.Name(), "_", 2)
				if len(parts) == 2 {
					item["owner"] = parts[0]
					item["name"] = parts[1]
				}
			}

			result = append(result, item)
		}

		if result == nil {
			result = []gin.H{}
		}
		c.JSON(http.StatusOK, result)
	})

	// 删除分享文件夹（需登录，只能删除自己的）
	r.DELETE("/api/delete-folder/:name", authMiddleware, func(c *gin.Context) {
		username := c.MustGet("username").(string)
		folderName := c.Param("name")
		folderName = strings.ReplaceAll(folderName, "..", "")
		folderName = strings.Trim(folderName, "/\\")
		targetDir := fmt.Sprintf("./shared/%s_%s", username, folderName)
		if err := os.RemoveAll(targetDir); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
	})

	// 嵌入的前端静态资源
	subFS, _ := fs.Sub(frontendStatic, "dist")
	r.NoRoute(func(c *gin.Context) {
		// 如果是 API 请求但没找到路由，由 Gin 处理 (404)
		// 否则尝试从嵌入文件系统中读
		fileServer := http.FileServer(http.FS(subFS))
		fileServer.ServeHTTP(c.Writer, c.Request)
	})

	// ====== 管理员接口 ======
	// 管理员鉴权中间件
	adminAuthMiddleware := func(c *gin.Context) {
		username := c.MustGet("username").(string)
		var user User
		if err := db.Where("username = ?", username).First(&user).Error; err != nil || (user.Role != "admin" && user.Role != "system") {
			c.JSON(http.StatusForbidden, gin.H{"error": "无管理员权限"})
			c.Abort()
			return
		}
		c.Set("role", user.Role)
		c.Next()
	}

	adminGroup := r.Group("/api/admin")
	adminGroup.Use(authMiddleware, adminAuthMiddleware)

	// 获取所有用户
	adminGroup.GET("/users", func(c *gin.Context) {
		var users []User
		db.Select("id", "created_at", "username", "avatar", "role", "is_muted", "is_banned").Find(&users)
		c.JSON(http.StatusOK, users)
	})

	// 切换禁言状态
	adminGroup.POST("/mute", func(c *gin.Context) {
		var req struct {
			Username string `json:"username" binding:"required"`
			IsMuted  bool   `json:"is_muted"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}
		callerRole := c.MustGet("role").(string)
		var target User
		if err := db.Where("username = ?", req.Username).First(&target).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}
		if callerRole == "admin" && target.Role != "user" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无法操作同级或更高级别用户"})
			return
		}
		if callerRole == "system" && target.Role == "system" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无法操作最高权限层级"})
			return
		}

		if err := db.Model(&User{}).Where("username = ?", req.Username).Update("is_muted", req.IsMuted).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "操作成功"})
	})

	// 切换封禁状态
	adminGroup.POST("/ban_user", func(c *gin.Context) {
		var req struct {
			Username string `json:"username" binding:"required"`
			IsBanned bool   `json:"is_banned"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}
		callerRole := c.MustGet("role").(string)
		var target User
		if err := db.Where("username = ?", req.Username).First(&target).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}
		if callerRole == "admin" && target.Role != "user" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无法操作同级或更高级别用户"})
			return
		}
		if callerRole == "system" && target.Role == "system" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无法操作最高权限层级"})
			return
		}

		if err := db.Model(&User{}).Where("username = ?", req.Username).Update("is_banned", req.IsBanned).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "操作成功"})
	})

	// 获取封禁 IP
	adminGroup.GET("/banned_ips", func(c *gin.Context) {
		var bans []IPBan
		db.Find(&bans)
		c.JSON(http.StatusOK, bans)
	})

	// 封禁/解封 IP
	adminGroup.POST("/ban_ip", func(c *gin.Context) {
		var req struct {
			IP     string `json:"ip" binding:"required"`
			Action string `json:"action" binding:"required"` // "ban", "unban"
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		if req.Action == "ban" {
			isRange := strings.Contains(req.IP, "/") || strings.Contains(req.IP, "-")
			db.Save(&IPBan{IP: req.IP, IsRange: isRange})
		} else {
			db.Where("ip = ?", req.IP).Delete(&IPBan{})
		}
		c.JSON(http.StatusOK, gin.H{"message": "操作成功"})
	})

	// 删除用户 (针对 User 或 Admin)
	adminGroup.DELETE("/users/:username", func(c *gin.Context) {
		targetUsername := c.Param("username")
		callerRole := c.MustGet("role").(string)

		var target User
		if err := db.Where("username = ?", targetUsername).First(&target).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}

		// 权限判别
		if callerRole == "admin" && target.Role != "user" {
			c.JSON(http.StatusForbidden, gin.H{"error": "普通管理员仅能删除普通用户"})
			return
		}
		if target.Role == "system" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无法删除系统最高权限者"})
			return
		}

		// 执行删除
		db.Delete(&target)
		c.JSON(http.StatusOK, gin.H{"message": "用户删除成功"})
	})

	// 修改管理员密码
	adminGroup.POST("/password", func(c *gin.Context) {
		var req struct {
			NewPassword string `json:"new_password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}
		db.Save(&Config{Key: "admin_password", Value: req.NewPassword})
		c.JSON(http.StatusOK, gin.H{"message": "管理员密码修改成功"})
	})

	// ====== System 级接口 ======
	// 分配或取消 Admin
	adminGroup.POST("/set_role", func(c *gin.Context) {
		callerRole := c.MustGet("role").(string)
		if callerRole != "system" {
			c.JSON(http.StatusForbidden, gin.H{"error": "只有 system 角色可执行此操作"})
			return
		}
		var req struct {
			Username string `json:"username" binding:"required"`
			Role     string `json:"role" binding:"required"` // "admin" 或是 "user"
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		var target User
		if err := db.Where("username = ?", req.Username).First(&target).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}
		if target.Role == "system" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无法干涉 system 的身份"})
			return
		}

		if err := db.Model(&User{}).Where("username = ?", req.Username).Update("role", req.Role).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "角色修改失败"})
			return
		}

		// 通知对方在线客户端热更新 Role
		for client := range hub.clients {
			if client.Username == req.Username {
				client.Role = req.Role
				client.send <- Message{
					Type: "role_update",
					Role: req.Role,
				}
			}
		}

		c.JSON(http.StatusOK, gin.H{"message": "角色分配成功"})
	})

	// 修改系统管理员密码
	adminGroup.POST("/system_password", func(c *gin.Context) {
		callerRole := c.MustGet("role").(string)
		if callerRole != "system" {
			c.JSON(http.StatusForbidden, gin.H{"error": "只有 system 角色可执行此操作"})
			return
		}
		var req struct {
			NewPassword string `json:"new_password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}
		db.Save(&Config{Key: "system_password", Value: req.NewPassword})
		c.JSON(http.StatusOK, gin.H{"message": "系统级密码修改成功"})
	})

	// 启动！！！！！
	fmt.Println("------------------------------")
	fmt.Println("🚀 AirChat 后端已成功启动！")
	fmt.Println("📍 监听端口: :8080")
	fmt.Println("🔗 WebSocket 入口: ws://127.0.0.1:8080/ws?token=YOUR_TOKEN")
	fmt.Println("------------------------------")

	r.Run(":8080")
}
