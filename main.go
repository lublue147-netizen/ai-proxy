package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	version = "1.0.0"
)

func main() {
	configPath := flag.String("c", "", "配置文件路径 (支持 yaml 或 json，默认自动寻找 config.yaml)")
	showVersion := flag.Bool("v", false, "显示版本号")
	flag.Parse()

	if *showVersion {
		fmt.Printf("AI Proxy v%s (Windows Compatible)\n", version)
		return
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[Error] 加载配置失败: %v", err)
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	handler := NewProxyHandler(cfg)

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  0, // 不限制读取时间，适应长对话及大文件
		WriteTimeout: 0, // 不限制写入时间，支持长流式 SSE
		IdleTimeout:  120 * time.Second,
	}

	fmt.Println("==================================================================")
	fmt.Printf("               AI Proxy 本地负载均衡代理 (v%s)            \n", version)
	fmt.Println("==================================================================")
	fmt.Printf("监听地址: http://%s\n", addr)
	fmt.Printf("状态监控: http://%s/_proxy/status\n", addr)
	fmt.Printf("已加载 %d 个上游 OpenAI 节点:\n", len(cfg.Upstreams))
	for i, u := range cfg.Upstreams {
		fmt.Printf("  [%d] %-15s (权重: %d) -> %s\n", i+1, u.Name, u.Weight, u.BaseURL)
	}
	fmt.Println("------------------------------------------------------------------")
	fmt.Println("服务已就绪，按 Ctrl+C 可停止代理")
	fmt.Println("==================================================================")

	// 优雅停机监听
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Error] 启动服务器异常: %v", err)
		}
	}()

	<-stopChan
	log.Println("[Info] 接收到退出信号，正在关闭代理服务...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("[Error] 服务关闭超时: %v", err)
	} else {
		log.Println("[Info] 服务已安全关闭。")
	}
}
