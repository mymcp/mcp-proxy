package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"

	"github.com/mymcp/mcp-proxy/pkg/common"
	"github.com/mymcp/mcp-proxy/pkg/proxy"
)

type ProxyConfig struct {
	SseAddr    string
	SseUrl     string
	McpServers map[string]*common.McpServerConfig
}

func main() {
	err := initProxy()
	if err != nil {
		log.Fatal(err)
	}
}

func initProxy() error {
	proxyConfig, err := loadConfig()
	if err != nil {
		return err
	}

	waitGroup := sync.WaitGroup{}
	waitGroup.Add(len(proxyConfig.McpServers))
	for name, config := range proxyConfig.McpServers {
		go func() {
			defer func() {
				e := recover()
				if e != nil {
					fmt.Println(e)
				}
				waitGroup.Done()
			}()
			basePath := name
			proxyServer, err := proxy.NewProxyServer(config)
			if err != nil {
				fmt.Println(err.Error())
				return
			}
			sseServer := proxy.NewSSEServer(proxyServer, proxy.WithBaseURL(proxyConfig.SseUrl), proxy.WithBasePath(basePath))

			http.Handle(sseServer.CompleteSsePath(), sseServer)
			http.Handle(sseServer.CompleteMessagePath(), sseServer)

			fmt.Println("serve " + sseServer.CompleteSseEndpoint() + " via " + fmt.Sprintf("%s", proxyServer.ServerInfo))
		}()
	}
	waitGroup.Wait()

	fmt.Println("listening on " + proxyConfig.SseAddr)
	err = http.ListenAndServe(proxyConfig.SseAddr, nil)
	if err != nil {
		return err
	}
	return nil
}

func loadConfig() (ProxyConfig, error) {
	cfg := ProxyConfig{}
	data, err := os.ReadFile("proxy.json")
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		return cfg, err
	}

	if cfg.SseAddr == "" && cfg.SseUrl == "" {
		cfg.SseAddr = "localhost:8080"
		cfg.SseUrl = "http://localhost:8080"
	} else if cfg.SseAddr == "" && cfg.SseUrl != "" {
		// parse host from sseUrl
		u, err := url.Parse(cfg.SseUrl)
		if err != nil || u.Host == "" {
			cfg.SseAddr = "localhost:8080"
		} else {
			host, port, splitErr := net.SplitHostPort(u.Host)
			if splitErr != nil {
				// 没有端口，根据 scheme 自动补全
				host = u.Host
				if u.Scheme == "https" {
					port = "443"
				} else {
					port = "80"
				}
				cfg.SseAddr = host + ":" + port
			} else {
				cfg.SseAddr = u.Host
			}
		}
	} else if cfg.SseUrl == "" && cfg.SseAddr != "" {
		cfg.SseUrl = "http://" + cfg.SseAddr
	}

	return cfg, nil
}
