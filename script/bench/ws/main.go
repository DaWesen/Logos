package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var (
	baseURL    string
	numConns   int
	duration   time.Duration
	msgRate    int
	msgContent string
)

func init() {
	flag.StringVar(&baseURL, "url", "http://localhost:8888", "Gateway base URL")
	flag.IntVar(&numConns, "conns", 50, "Number of WebSocket connections")
	flag.DurationVar(&duration, "duration", 30*time.Second, "Test duration")
	flag.IntVar(&msgRate, "rate", 1, "Messages per second per connection")
	flag.StringVar(&msgContent, "msg", "benchmark test message", "Message content")
	flag.Parse()
}

type WSResult struct {
	TotalConns    int64
	Connected     int64
	ConnectFailed int64
	MessagesSent  int64
	MessagesRecv  int64
	SendErrors    int64
	RecvErrors    int64
	Latencies     []time.Duration
	mu            sync.Mutex
}

func (r *WSResult) Print() {
	fmt.Println("\n========== WebSocket 压测结果 ==========")
	fmt.Printf("目标连接数:   %d\n", numConns)
	fmt.Printf("成功连接:     %d\n", atomic.LoadInt64(&r.Connected))
	fmt.Printf("连接失败:     %d\n", atomic.LoadInt64(&r.ConnectFailed))
	fmt.Printf("发送消息:     %d\n", atomic.LoadInt64(&r.MessagesSent))
	fmt.Printf("接收消息:     %d\n", atomic.LoadInt64(&r.MessagesRecv))
	fmt.Printf("发送错误:     %d\n", atomic.LoadInt64(&r.SendErrors))
	fmt.Printf("接收错误:     %d\n", atomic.LoadInt64(&r.RecvErrors))

	r.mu.Lock()
	if len(r.Latencies) > 0 {
		durations := make([]time.Duration, len(r.Latencies))
		copy(durations, r.Latencies)
		r.mu.Unlock()

		for i := 0; i < len(durations); i++ {
			for j := i + 1; j < len(durations); j++ {
				if durations[j] < durations[i] {
					durations[i], durations[j] = durations[j], durations[i]
				}
			}
		}

		var total time.Duration
		for _, d := range durations {
			total += d
		}

		fmt.Println("\n--- 消息延迟分布 ---")
		fmt.Printf("  平均:  %v\n", total/time.Duration(len(durations)))
		fmt.Printf("  最小:  %v\n", durations[0])
		fmt.Printf("  最大:  %v\n", durations[len(durations)-1])
		fmt.Printf("  P50:   %v\n", durations[len(durations)*50/100])
		fmt.Printf("  P90:   %v\n", durations[len(durations)*90/100])
		fmt.Printf("  P95:   %v\n", durations[len(durations)*95/100])
		fmt.Printf("  P99:   %v\n", durations[len(durations)*99/100])
		fmt.Printf("  消息吞吐: %.1f msg/s\n", float64(atomic.LoadInt64(&r.MessagesRecv))/duration.Seconds())
	} else {
		r.mu.Unlock()
	}
	fmt.Println("========================================")
}

type WSMessage struct {
	Type      string      `json:"type"`
	RequestID string      `json:"request_id"`
	Data      interface{} `json:"data"`
}

func getToken(client *http.Client, id int) (string, int64, error) {
	username := fmt.Sprintf("wsbench_%d_%d", id, time.Now().UnixNano())
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": "Bench123456!",
	})

	resp, err := client.Post(baseURL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return "", 0, fmt.Errorf("register failed")
	}

	token, _ := data["token"].(string)
	userID := int64(0)
	if user, ok := data["user"].(map[string]interface{}); ok {
		if id, ok := user["id"].(float64); ok {
			userID = int64(id)
		}
	}
	return token, userID, nil
}

func runWSConnection(id int, token string, userID int64, result *WSResult, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	u, _ := url.Parse(baseURL)
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/ws?token=%s", scheme, u.Host, url.QueryEscape(token))

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		atomic.AddInt64(&result.ConnectFailed, 1)
		return
	}
	defer conn.Close()
	atomic.AddInt64(&result.Connected, 1)

	conn.SetPingHandler(func(appData string) error {
		conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second*5))
		return nil
	})

	msgCh := make(chan []byte, 100)

	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				_, message, err := conn.ReadMessage()
				if err != nil {
					atomic.AddInt64(&result.RecvErrors, 1)
					return
				}
				var msg map[string]interface{}
				json.Unmarshal(message, &msg)
				msgType, _ := msg["type"].(string)
				if msgType == "ack" || msgType == "message" {
					atomic.AddInt64(&result.MessagesRecv, 1)
				}
				if msgType == "ack" {
					if reqID, ok := msg["request_id"].(string); ok {
						select {
						case msgCh <- []byte(reqID):
						default:
						}
					}
				}
			}
		}
	}()

	pendingRequests := make(map[string]time.Time)
	var pendingMu sync.Mutex
	interval := time.Second / time.Duration(msgRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	reqCounter := int64(0)

	for {
		select {
		case <-stop:
			return
		case reqIDBytes := <-msgCh:
			reqID := string(reqIDBytes)
			pendingMu.Lock()
			if sentAt, ok := pendingRequests[reqID]; ok {
				latency := time.Since(sentAt)
				delete(pendingRequests, reqID)
				pendingMu.Unlock()
				result.mu.Lock()
				result.Latencies = append(result.Latencies, latency)
				result.mu.Unlock()
			} else {
				pendingMu.Unlock()
			}
		case <-ticker.C:
			reqID := fmt.Sprintf("ws-%d-%d", id, atomic.AddInt64(&reqCounter, 1))
			msg := WSMessage{
				Type:      "message",
				RequestID: reqID,
				Data: map[string]interface{}{
					"chat_id":      fmt.Sprintf("%d_1", userID),
					"content":      fmt.Sprintf("%s_%d", msgContent, time.Now().UnixNano()),
					"chat_type":    1,
					"message_type": 1,
				},
			}
			payload, _ := json.Marshal(msg)

			pendingMu.Lock()
			pendingRequests[reqID] = time.Now()
			pendingMu.Unlock()

			err := conn.WriteMessage(websocket.TextMessage, payload)
			if err != nil {
				atomic.AddInt64(&result.SendErrors, 1)
				pendingMu.Lock()
				delete(pendingRequests, reqID)
				pendingMu.Unlock()
			} else {
				atomic.AddInt64(&result.MessagesSent, 1)
			}
		}
	}
}

func main() {
	fmt.Println("=========================================")
	fmt.Println("  Logos WebSocket 压测工具")
	fmt.Println("=========================================")
	fmt.Printf("目标: %s\n", baseURL)
	fmt.Printf("连接数: %d\n", numConns)
	fmt.Printf("持续时间: %v\n", duration)
	fmt.Printf("每连接消息率: %d msg/s\n", msgRate)
	fmt.Println()

	result := &WSResult{}

	fmt.Println("[1/3] 获取测试 Token...")
	tokens := make([]struct {
		Token  string
		UserID int64
	}, numConns)
	client := &http.Client{Timeout: 10 * time.Second}
	var wg sync.WaitGroup

	for i := 0; i < numConns; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			token, userID, err := getToken(client, idx)
			if err != nil {
				fmt.Printf("  获取 Token %d 失败: %v\n", idx, err)
				return
			}
			tokens[idx].Token = token
			tokens[idx].UserID = userID
		}(i)
		if (i+1)%50 == 0 {
			wg.Wait()
		}
	}
	wg.Wait()

	validConns := 0
	for _, t := range tokens {
		if t.Token != "" {
			validConns++
		}
	}
	fmt.Printf("  成功获取 %d 个 Token\n\n", validConns)

	if validConns == 0 {
		fmt.Println("没有可用的 Token，退出")
		return
	}

	fmt.Println("[2/3] 建立 WebSocket 连接并压测...")
	stop := make(chan struct{})
	time.AfterFunc(duration, func() { close(stop) })

	startTime := time.Now()
	for i, t := range tokens {
		if t.Token == "" {
			continue
		}
		wg.Add(1)
		go runWSConnection(i, t.Token, t.UserID, result, stop, &wg)
	}

	statTicker := time.NewTicker(5 * time.Second)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-statTicker.C:
				elapsed := time.Since(startTime)
				fmt.Printf("  [%v] 连接: %d | 发送: %d | 接收: %d | 吞吐: %.1f msg/s\n",
					elapsed.Round(time.Second),
					atomic.LoadInt64(&result.Connected),
					atomic.LoadInt64(&result.MessagesSent),
					atomic.LoadInt64(&result.MessagesRecv),
					float64(atomic.LoadInt64(&result.MessagesRecv))/elapsed.Seconds())
			}
		}
	}()

	wg.Wait()

	fmt.Println("\n[3/3] 压测完成")
	result.Print()
}
