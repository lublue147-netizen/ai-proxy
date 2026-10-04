package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// UpstreamNode 表示运行时的上游节点
type UpstreamNode struct {
	Config          UpstreamConfig
	ParsedURL       *url.URL
	CurrentWeight   int
	EffectiveWeight int
	Weight          int
	TotalRequests   int64
	SuccessRequests int64
	FailedRequests  int64
	LastUsed        time.Time
	mu              sync.Mutex
}

// LoadBalancer 加权轮询负载均衡器 (Nginx Smooth Weighted Round-Robin)
type LoadBalancer struct {
	nodes []*UpstreamNode
	mu    sync.Mutex
}

// NewLoadBalancer 创建负载均衡器
func NewLoadBalancer(upstreams []UpstreamConfig) *LoadBalancer {
	nodes := make([]*UpstreamNode, len(upstreams))
	for i, u := range upstreams {
		weight := u.Weight
		if weight <= 0 {
			weight = 1
		}
		nodes[i] = &UpstreamNode{
			Config:          u,
			ParsedURL:       u.ParsedURL,
			CurrentWeight:   0,
			EffectiveWeight: weight,
			Weight:          weight,
		}
	}
	return &LoadBalancer{nodes: nodes}
}

// Next 选择下一个可用节点（平滑加权轮询）
func (lb *LoadBalancer) Next() *UpstreamNode {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	if len(lb.nodes) == 0 {
		return nil
	}
	if len(lb.nodes) == 1 {
		node := lb.nodes[0]
		atomic.AddInt64(&node.TotalRequests, 1)
		node.LastUsed = time.Now()
		return node
	}

	totalWeight := 0
	var best *UpstreamNode

	for _, node := range lb.nodes {
		node.CurrentWeight += node.EffectiveWeight
		totalWeight += node.EffectiveWeight

		if node.EffectiveWeight < node.Weight {
			node.EffectiveWeight++
		}

		if best == nil || node.CurrentWeight > best.CurrentWeight {
			best = node
		}
	}

	if best == nil {
		return nil
	}

	best.CurrentWeight -= totalWeight
	atomic.AddInt64(&best.TotalRequests, 1)
	best.LastUsed = time.Now()
	return best
}

// ProxyHandler 代理服务处理器
type ProxyHandler struct {
	cfg          *Config
	lb           *LoadBalancer
	client       *http.Client
	reverseProxy *httputil.ReverseProxy
}

// NewProxyHandler 创建代理处理器
func NewProxyHandler(cfg *Config) *ProxyHandler {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 0, // 适应大模型生成长响应
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   0, // 0 表示无全局超时，支持长连接流式输出
	}

	h := &ProxyHandler{
		cfg:    cfg,
		lb:     NewLoadBalancer(cfg.Upstreams),
		client: client,
	}

	return h
}

// joinURL 智能拼接 Base URL 和请求 Path
func joinURL(baseURL *url.URL, reqURL *url.URL) *url.URL {
	target := *baseURL

	basePath := strings.TrimRight(baseURL.Path, "/")
	reqPath := reqURL.Path

	// 智能去重：如果 base_url 包含 /v1，且客户端请求也是以 /v1 开头，避免 /v1/v1
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(reqPath, "/v1") {
		reqPath = strings.TrimPrefix(reqPath, "/v1")
	}

	if !strings.HasPrefix(reqPath, "/") && reqPath != "" {
		reqPath = "/" + reqPath
	}

	target.Path = basePath + reqPath
	target.RawQuery = reqURL.RawQuery
	return &target
}

// ServeHTTP 处理客户端请求
func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. 状态与健康检查端点
	if r.URL.Path == "/_proxy/status" || r.URL.Path == "/health" {
		h.handleStatus(w, r)
		return
	}

	// 2. 校验访问本代理的 ClientKey（如果配置了）
	if h.cfg.Server.ClientKey != "" {
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != h.cfg.Server.ClientKey && r.Header.Get("X-Proxy-Key") != h.cfg.Server.ClientKey {
			http.Error(w, `{"error":{"message":"Invalid proxy client key","type":"proxy_error"}}`, http.StatusUnauthorized)
			return
		}
	}

	// 3. 读取并备份请求 Body（支持重试）
	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":{"message":"Failed to read request body: %s"}}`, err.Error()), http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()
	}

	maxRetries := h.cfg.Server.RetryCount
	if maxRetries < 0 {
		maxRetries = 0
	}

	var lastErr error
	var lastStatusCode int

	for attempt := 0; attempt <= maxRetries; attempt++ {
		node := h.lb.Next()
		if node == nil {
			http.Error(w, `{"error":{"message":"No available upstreams"}}`, http.StatusServiceUnavailable)
			return
		}

		targetURL := joinURL(node.ParsedURL, r.URL)

		if h.cfg.Server.Verbose {
			log.Printf("[Proxy] %s %s -> %s (node: %s, attempt: %d)", r.Method, r.URL.Path, targetURL.String(), node.Config.Name, attempt+1)
		}

		// 构建上游请求
		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		outReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), reqBody)
		if err != nil {
			lastErr = err
			atomic.AddInt64(&node.FailedRequests, 1)
			continue
		}

		// 4. Header 处理：
		// 首先透传客户端的所有 Header
		for k, vv := range r.Header {
			// 跳过逐跳 Header
			if isHopByHopHeader(k) {
				continue
			}
			for _, v := range vv {
				outReq.Header.Add(k, v)
			}
		}

		// 覆盖写入上游配置的 Header（如自定义 Authorization、X-Custom 等）
		for k, v := range node.Config.Headers {
			outReq.Header.Set(k, v)
		}

		// 设置 Host 为上游主机名，防止 upstream 反代/CDN 403 阻断
		outReq.Host = node.ParsedURL.Host

		// 设置 X-Forwarded-* 头部
		if clientIP, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			if prior, ok := outReq.Header["X-Forwarded-For"]; ok {
				clientIP = strings.Join(prior, ", ") + ", " + clientIP
			}
			outReq.Header.Set("X-Forwarded-For", clientIP)
		}
		outReq.Header.Set("X-Forwarded-Proto", "http")

		// 5. 执行请求
		resp, err := h.client.Do(outReq)
		if err != nil {
			lastErr = err
			atomic.AddInt64(&node.FailedRequests, 1)
			if h.cfg.Server.Verbose {
				log.Printf("[Proxy Error] node %s failed: %v", node.Config.Name, err)
			}
			// 发生连接层错误，如果还有重试机会，则换下一个节点
			if attempt < maxRetries && r.Context().Err() == nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			http.Error(w, fmt.Sprintf(`{"error":{"message":"Proxy request failed: %s","type":"proxy_error"}}`, err.Error()), http.StatusBadGateway)
			return
		}

		// 如果上游返回 502/503/504 等网关错误且还有重试次数，尝试下一个 upstream
		if (resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout) && attempt < maxRetries && r.Context().Err() == nil {
			_ = resp.Body.Close()
			lastStatusCode = resp.StatusCode
			atomic.AddInt64(&node.FailedRequests, 1)
			if h.cfg.Server.Verbose {
				log.Printf("[Proxy Retry] node %s returned %d, retrying next node...", node.Config.Name, resp.StatusCode)
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// 请求成功
		atomic.AddInt64(&node.SuccessRequests, 1)
		defer resp.Body.Close()

		// 6. 将响应头拷贝回客户端
		copyHeader(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)

		// 7. 处理流式输出 (SSE 针对 OpenAI 流式请求及时 Flush)
		flusher, isFlusher := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				_, writeErr := w.Write(buf[:n])
				if writeErr != nil {
					// 客户端断开连接
					return
				}
				if isFlusher {
					flusher.Flush()
				}
			}
			if readErr != nil {
				if readErr != io.EOF && h.cfg.Server.Verbose {
					log.Printf("[Proxy Stream] Read error: %v", readErr)
				}
				break
			}
		}
		return
	}

	if lastErr != nil {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"All upstream attempts failed: %s","type":"proxy_error"}}`, lastErr.Error()), http.StatusBadGateway)
	} else {
		http.Error(w, fmt.Sprintf(`{"error":{"message":"All upstream attempts returned error status %d","type":"proxy_error"}}`, lastStatusCode), http.StatusBadGateway)
	}
}

// handleStatus 返回所有 upstream 的运行统计与健康状态
func (h *ProxyHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	type NodeStatus struct {
		Name            string            `json:"name"`
		BaseURL         string            `json:"base_url"`
		Weight          int               `json:"weight"`
		CurrentWeight   int               `json:"current_weight"`
		TotalRequests   int64             `json:"total_requests"`
		SuccessRequests int64             `json:"success_requests"`
		FailedRequests  int64             `json:"failed_requests"`
		LastUsed        string            `json:"last_used,omitempty"`
		OverrideHeaders map[string]string `json:"override_headers"`
	}

	h.lb.mu.Lock()
	nodesInfo := make([]NodeStatus, len(h.lb.nodes))
	for i, n := range h.lb.nodes {
		lastUsedStr := ""
		if !n.LastUsed.IsZero() {
			lastUsedStr = n.LastUsed.Format("2006-01-02 15:04:05")
		}
		// 掩码处理 Authorization header 防止泄露完整 key
		maskedHeaders := make(map[string]string)
		for k, v := range n.Config.Headers {
			if strings.EqualFold(k, "Authorization") && len(v) > 12 {
				maskedHeaders[k] = v[:7] + "..." + v[len(v)-4:]
			} else {
				maskedHeaders[k] = v
			}
		}

		nodesInfo[i] = NodeStatus{
			Name:            n.Config.Name,
			BaseURL:         n.Config.BaseURL,
			Weight:          n.Weight,
			CurrentWeight:   n.CurrentWeight,
			TotalRequests:   atomic.LoadInt64(&n.TotalRequests),
			SuccessRequests: atomic.LoadInt64(&n.SuccessRequests),
			FailedRequests:  atomic.LoadInt64(&n.FailedRequests),
			LastUsed:        lastUsedStr,
			OverrideHeaders: maskedHeaders,
		}
	}
	h.lb.mu.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "running",
		"time":      time.Now().Format("2006-01-02 15:04:05"),
		"upstreams": nodesInfo,
	})
}

// 拷贝 HTTP Header
func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		if isHopByHopHeader(k) {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// 判断是否为 HTTP Hop-by-hop 头
func isHopByHopHeader(header string) bool {
	switch strings.ToLower(header) {
	case "connection",
		"keep-alive",
		"proxy-authenticate",
		"proxy-authorization",
		"te",
		"trailers",
		"transfer-encoding",
		"upgrade":
		return true
	default:
		return false
	}
}
