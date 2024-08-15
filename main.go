package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// 允许跨域请求
		return true
	},
}

// 维护每篇文章的 WebSocket 连接
var clients = make(map[string]map[*websocket.Conn]bool)

var redisClient *redis.Client
var ctx = context.Background()

// Message 是传递的消息结构体
type Message struct {
	ArticleID string `json:"article_id"` // 文章ID
	Content   string `json:"content"`    // 文章内容
}

func init() {
	// 初始化 Redis 客户端
	redisClient = redis.NewClient(&redis.Options{
		Addr:     os.Getenv("REDIS_ADDR"),
		Password: "", // 没有密码则保持为空
		DB:       0,  // 使用默认数据库
	})
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	// 将HTTP连接升级为WebSocket连接
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ws.Close()

	// 解析文章ID
	articleID := r.URL.Query().Get("article_id")
	if articleID == "" {
		log.Println("没有提供文章ID")
		return
	}

	// 初始化文章的客户端列表
	if clients[articleID] == nil {
		clients[articleID] = make(map[*websocket.Conn]bool)
	}

	clients[articleID][ws] = true

	for {
		var msg Message
		// 读取新消息
		err := ws.ReadJSON(&msg)
		if err != nil {
			log.Printf("error: %v", err)
			delete(clients[articleID], ws)
			break
		}

		// 将收到的消息发布到 Redis 的 Pub/Sub 频道
		err = publishToRedis(msg)
		if err != nil {
			log.Printf("Redis 发布错误: %v", err)
		}
	}
}

func handleRedisMessages() {
	subscriber := redisClient.Subscribe(ctx, "article_updates")

	// 订阅频道消息
	for msg := range subscriber.Channel() {
		var receivedMsg Message
		err := json.Unmarshal([]byte(msg.Payload), &receivedMsg)
		if err != nil {
			log.Printf("消息解析错误: %v", err)
			continue
		}

		// 获取消息对应文章的客户端列表
		articleClients := clients[receivedMsg.ArticleID]
		if articleClients != nil {
			// 将消息发送给对应文章的所有连接客户端
			for client := range articleClients {
				err := client.WriteJSON(receivedMsg)
				if err != nil {
					log.Printf("WebSocket 发送错误: %v", err)
					client.Close()
					delete(articleClients, client)
				}
			}
		}
	}
}

func publishToRedis(msg Message) error {
	messageBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// 将消息发布到 Redis 频道
	return redisClient.Publish(ctx, "article_updates", messageBytes).Err()
}

func main() {
	// 创建简单的文件服务器
	fs := http.FileServer(http.Dir("./public"))
	http.Handle("/", fs)

	// 处理 WebSocket 请求
	http.HandleFunc("/ws", handleConnections)

	// 启动处理 Redis 消息的 goroutine
	go handleRedisMessages()

	// 启动服务器
	fmt.Println("服务器启动在 :8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
