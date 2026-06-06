package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var (
	baseURL    string
	numUsers   int
	duration   time.Duration
	rpsPerUser int
)

func init() {
	flag.StringVar(&baseURL, "url", "http://localhost:8888", "Gateway base URL")
	flag.IntVar(&numUsers, "users", 100, "Number of concurrent users")
	flag.DurationVar(&duration, "duration", 30*time.Second, "Test duration")
	flag.IntVar(&rpsPerUser, "rps", 1, "Requests per second per user")
	flag.Parse()
}

type Result struct {
	Total      int64
	Success    int64
	Failed     int64
	Timeout    int64
	StatusCodes map[int]int64
	Latencies  []time.Duration
	Errors     map[string]int
	mu         sync.Mutex
}

func NewResult() *Result {
	return &Result{
		StatusCodes: make(map[int]int64),
		Latencies:   make([]time.Duration, 0, 10000),
		Errors:      make(map[string]int),
	}
}

func (r *Result) Record(statusCode int, latency time.Duration, err error) {
	atomic.AddInt64(&r.Total, 1)
	if err != nil {
		atomic.AddInt64(&r.Failed, 1)
		if _, ok := err.(interface{ Timeout() bool }); ok || err.Error() == "context deadline exceeded" {
			atomic.AddInt64(&r.Timeout, 1)
		}
		r.mu.Lock()
		r.Errors[err.Error()]++
		r.mu.Unlock()
	} else {
		atomic.AddInt64(&r.Success, 1)
		r.mu.Lock()
		r.StatusCodes[statusCode]++
		r.Latencies = append(r.Latencies, latency)
		r.mu.Unlock()
	}
}

func (r *Result) Print() {
	fmt.Println("\n========== 压测结果 ==========")
	fmt.Printf("总请求数:     %d\n", r.Total)
	fmt.Printf("成功:         %d\n", r.Success)
	fmt.Printf("失败:         %d\n", r.Failed)
	fmt.Printf("超时:         %d\n", r.Timeout)
	if r.Total > 0 {
		fmt.Printf("成功率:       %.2f%%\n", float64(r.Success)/float64(r.Total)*100)
	}

	fmt.Println("\n--- 状态码分布 ---")
	for code, count := range r.StatusCodes {
		fmt.Printf("  %d: %d (%.1f%%)\n", code, count, float64(count)/float64(r.Total)*100)
	}

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

		fmt.Println("\n--- 延迟分布 ---")
		fmt.Printf("  平均:  %v\n", total/time.Duration(len(durations)))
		fmt.Printf("  最小:  %v\n", durations[0])
		fmt.Printf("  最大:  %v\n", durations[len(durations)-1])
		fmt.Printf("  P50:   %v\n", durations[len(durations)*50/100])
		fmt.Printf("  P90:   %v\n", durations[len(durations)*90/100])
		fmt.Printf("  P95:   %v\n", durations[len(durations)*95/100])
		fmt.Printf("  P99:   %v\n", durations[len(durations)*99/100])
		fmt.Printf("  QPS:   %.1f\n", float64(r.Success)/duration.Seconds())
	} else {
		r.mu.Unlock()
	}

	if len(r.Errors) > 0 {
		fmt.Println("\n--- 错误分布 ---")
		for err, count := range r.Errors {
			fmt.Printf("  %s: %d\n", err, count)
		}
	}
	fmt.Println("==============================")
}

type TestUser struct {
	ID       int64
	Username string
	Token    string
	Client   *http.Client
}

func registerUser(client *http.Client, id int) (*TestUser, error) {
	username := fmt.Sprintf("benchuser_%d_%d", id, time.Now().UnixNano())
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": "Bench123456!",
	})

	resp, err := client.Post(baseURL+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("register failed: %v", result)
	}

	token, _ := data["token"].(string)
	userID := float64(0)
	if user, ok := data["user"].(map[string]interface{}); ok {
		if id, ok := user["id"].(float64); ok {
			userID = id
		}
	}

	return &TestUser{
		ID:       int64(userID),
		Username: username,
		Token:    token,
		Client:   client,
	}, nil
}

func loginUser(client *http.Client, username, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})

	resp, err := client.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("login failed: %v", result)
	}

	token, _ := data["token"].(string)
	return token, nil
}

func (u *TestUser) doRequest(method, path string, body io.Reader) (int, time.Duration, error) {
	req, err := http.NewRequest(method, baseURL+path, body)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+u.Token)
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := u.Client.Do(req)
	latency := time.Since(start)

	if err != nil {
		return 0, latency, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, latency, nil
}

type Scenario func(user *TestUser, result *Result)

func healthCheckScenario(user *TestUser, result *Result) {
	start := time.Now()
	resp, err := http.Get(baseURL + "/health")
	latency := time.Since(start)
	if err != nil {
		result.Record(0, latency, err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	result.Record(resp.StatusCode, latency, nil)
}

func loginScenario(user *TestUser, result *Result) {
	start := time.Now()
	body, _ := json.Marshal(map[string]string{
		"username": user.Username,
		"password": "Bench123456!",
	})
	resp, err := user.Client.Post(baseURL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	latency := time.Since(start)
	if err != nil {
		result.Record(0, latency, err)
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	result.Record(resp.StatusCode, latency, nil)
}

func getUserScenario(user *TestUser, result *Result) {
	statusCode, latency, err := user.doRequest("GET", fmt.Sprintf("/api/v1/users/%d", user.ID), nil)
	result.Record(statusCode, latency, err)
}

func getChatHistoryScenario(user *TestUser, result *Result) {
	statusCode, latency, err := user.doRequest("GET", "/api/v1/chat/history?chat_id=1_2&limit=20", nil)
	result.Record(statusCode, latency, err)
}

func sendChatMessageScenario(user *TestUser, result *Result) {
	body, _ := json.Marshal(map[string]interface{}{
		"chat_id":      fmt.Sprintf("%d_1", user.ID),
		"content":      fmt.Sprintf("bench message %d", time.Now().UnixNano()),
		"chat_type":    1,
		"message_type": 1,
	})
	statusCode, latency, err := user.doRequest("POST", "/api/v1/chat/message", bytes.NewReader(body))
	result.Record(statusCode, latency, err)
}

func getBotListScenario(user *TestUser, result *Result) {
	statusCode, latency, err := user.doRequest("GET", "/api/v1/bot", nil)
	result.Record(statusCode, latency, err)
}

func getBillingAccountScenario(user *TestUser, result *Result) {
	statusCode, latency, err := user.doRequest("GET", "/api/v1/billing/account", nil)
	result.Record(statusCode, latency, err)
}

func getMonitoringServicesScenario(user *TestUser, result *Result) {
	statusCode, latency, err := user.doRequest("GET", "/api/v1/monitoring/service-status/list", nil)
	result.Record(statusCode, latency, err)
}

func getContactListScenario(user *TestUser, result *Result) {
	statusCode, latency, err := user.doRequest("GET", "/api/v1/contact/list", nil)
	result.Record(statusCode, latency, err)
}

func mixedScenario(user *TestUser, result *Result) {
	scenarios := []Scenario{
		getUserScenario,
		getChatHistoryScenario,
		getBotListScenario,
		getBillingAccountScenario,
		getContactListScenario,
	}
	idx := time.Now().UnixNano() % int64(len(scenarios))
	scenarios[idx](user, result)
}

var scenarios = map[string]Scenario{
	"health":     healthCheckScenario,
	"login":      loginScenario,
	"get_user":   getUserScenario,
	"chat_history": getChatHistoryScenario,
	"send_message": sendChatMessageScenario,
	"bot_list":   getBotListScenario,
	"billing":    getBillingAccountScenario,
	"monitoring": getMonitoringServicesScenario,
	"contacts":   getContactListScenario,
	"mixed":      mixedScenario,
}

func main() {
	fmt.Println("=========================================")
	fmt.Println("  Logos HTTP API 压测工具")
	fmt.Println("=========================================")
	fmt.Printf("目标: %s\n", baseURL)
	fmt.Printf("并发用户: %d\n", numUsers)
	fmt.Printf("持续时间: %v\n", duration)
	fmt.Printf("每用户RPS: %d\n", rpsPerUser)
	fmt.Println()

	scenarioName := "mixed"
	if flag.NArg() > 0 {
		scenarioName = flag.Arg(0)
	}
	scenario, ok := scenarios[scenarioName]
	if !ok {
		fmt.Printf("未知场景: %s\n可用场景: ", scenarioName)
		for name := range scenarios {
			fmt.Printf("%s ", name)
		}
		fmt.Println()
		return
	}
	fmt.Printf("压测场景: %s\n\n", scenarioName)

	result := NewResult()

	fmt.Println("[1/3] 注册测试用户...")
	users := make([]*TestUser, 0, numUsers)
	client := &http.Client{Timeout: 10 * time.Second}
	var wg sync.WaitGroup

	for i := 0; i < numUsers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			user, err := registerUser(client, idx)
			if err != nil {
				fmt.Printf("  注册用户 %d 失败: %v\n", idx, err)
				return
			}
			result.mu.Lock()
			users = append(users, user)
			result.mu.Unlock()
		}(i)
		if (i+1)%50 == 0 {
			wg.Wait()
			fmt.Printf("  已注册 %d/%d 用户\n", i+1, numUsers)
		}
	}
	wg.Wait()
	fmt.Printf("  成功注册 %d 个用户\n\n", len(users))

	if len(users) == 0 {
		fmt.Println("没有可用的测试用户，退出")
		return
	}

	fmt.Println("[2/3] 执行压测...")
	stop := make(chan struct{})
	time.AfterFunc(duration, func() { close(stop) })

	interval := time.Second / time.Duration(rpsPerUser)
	startTime := time.Now()

	for _, user := range users {
		wg.Add(1)
		go func(u *TestUser) {
			defer wg.Done()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					scenario(u, result)
				}
			}
		}(user)
	}

	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				elapsed := time.Since(startTime)
				fmt.Printf("  [%v] 请求: %d | 成功: %d | 失败: %d | QPS: %.1f\n",
					elapsed.Round(time.Second),
					atomic.LoadInt64(&result.Total),
					atomic.LoadInt64(&result.Success),
					atomic.LoadInt64(&result.Failed),
					float64(atomic.LoadInt64(&result.Success))/elapsed.Seconds())
			}
		}
	}()

	wg.Wait()

	fmt.Println("\n[3/3] 压测完成")
	result.Print()

	cleanupUsers(users)
}

func cleanupUsers(users []*TestUser) {
	fmt.Println("\n提示: 测试用户数据保留在数据库中，如需清理请手动删除 benchuser_* 用户")
	_ = users
}
