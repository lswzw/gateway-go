package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"gateway-go/internal/config"
	"gateway-go/internal/plugin"
)

// TestTimeoutApplied 验证 http.Server 正确应用了配置中的超时与请求头大小限制
func TestTimeoutApplied(t *testing.T) {
	t.Run("AllTimeoutsExplicitlyConfigured", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.ServerConfig{
				Port:                    8080,
				ReadTimeout:             10 * time.Second,
				ReadHeaderTimeout:       4 * time.Second,
				WriteTimeout:            15 * time.Second,
				IdleTimeout:             30 * time.Second,
				MaxHeaderBytes:          2048,
				GracefulShutdownTimeout: 5 * time.Second,
			},
		}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
		srv := newHTTPServer(cfg, handler)

		if srv.ReadTimeout != 10*time.Second {
			t.Errorf("expected ReadTimeout 10s, got %v", srv.ReadTimeout)
		}
		if srv.ReadHeaderTimeout != 4*time.Second {
			t.Errorf("expected ReadHeaderTimeout 4s, got %v", srv.ReadHeaderTimeout)
		}
		if srv.WriteTimeout != 15*time.Second {
			t.Errorf("expected WriteTimeout 15s, got %v", srv.WriteTimeout)
		}
		if srv.IdleTimeout != 30*time.Second {
			t.Errorf("expected IdleTimeout 30s, got %v", srv.IdleTimeout)
		}
		if srv.MaxHeaderBytes != 2048 {
			t.Errorf("expected MaxHeaderBytes 2048, got %v", srv.MaxHeaderBytes)
		}
	})

	t.Run("DefaultReadHeaderTimeoutIsHalfOfReadTimeout", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.ServerConfig{
				Port:              8080,
				ReadTimeout:       8 * time.Second,
				ReadHeaderTimeout: 0, // 未显式配置
				WriteTimeout:      8 * time.Second,
				IdleTimeout:       16 * time.Second,
				MaxHeaderBytes:    4096,
			},
		}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
		srv := newHTTPServer(cfg, handler)

		expectedHeaderTimeout := 4 * time.Second
		if srv.ReadHeaderTimeout != expectedHeaderTimeout {
			t.Errorf("expected ReadHeaderTimeout %v, got %v", expectedHeaderTimeout, srv.ReadHeaderTimeout)
		}
	})
}

// TestIdempotentShutdown 验证多次调用优雅关闭不会 panic
func TestIdempotentShutdown(t *testing.T) {
	port := getFreePort(t)
	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:                    port,
			GracefulShutdownTimeout: 2 * time.Second,
		},
	}
	pm := plugin.NewManager()
	srv := newHTTPServer(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	gs := NewGracefulServer(srv, pm, nil, cfg.Server.GracefulShutdownTimeout)

	// 启动服务器协程
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	waitForServerReady(t, port)

	// 顺序多次调用 Shutdown，不应该 panic
	for i := 0; i < 3; i++ {
		err := gs.Shutdown(context.Background())
		if err != nil {
			t.Errorf("unexpected error on shutdown iteration %d: %v", i, err)
		}
	}

	// 并发多次调用 Shutdown，不应该 panic
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = gs.Shutdown(context.Background())
		}()
	}
	wg.Wait()
}

// TestGracefulShutdown 验证在途请求在 SIGTERM 信号后仍能完成并返回 200，且在 timeout + 1s 内退出
func TestGracefulShutdown(t *testing.T) {
	port := getFreePort(t)
	gracefulTimeout := 2 * time.Second

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:                    port,
			ReadTimeout:             5 * time.Second,
			WriteTimeout:            5 * time.Second,
			GracefulShutdownTimeout: gracefulTimeout,
		},
	}

	inFlightEntered := make(chan struct{})
	inFlightDone := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow":
			close(inFlightEntered)
			time.Sleep(500 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("slow-response-ok"))
			close(inFlightDone)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}
	})

	srv := newHTTPServer(cfg, handler)
	pm := plugin.NewManager()

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- runServer(context.Background(), srv, cfg, pm, nil)
	}()

	waitForServerReady(t, port)

	// 发起在途慢请求
	type reqResult struct {
		statusCode int
		body       string
		err        error
	}
	clientResCh := make(chan reqResult, 1)

	client := &http.Client{Timeout: 5 * time.Second}
	go func() {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/slow", port))
		if err != nil {
			clientResCh <- reqResult{err: err}
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		clientResCh <- reqResult{statusCode: resp.StatusCode, body: string(body)}
	}()

	// 等待在途请求已经进入处理函数
	select {
	case <-inFlightEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for slow handler to be entered")
	}

	// 触发 SIGTERM 信号
	shutdownStart := time.Now()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("failed to send SIGTERM: %v", err)
	}

	// 断言在途请求正常返回 HTTP 200
	select {
	case res := <-clientResCh:
		if res.err != nil {
			t.Fatalf("in-flight request failed: %v", res.err)
		}
		if res.statusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", res.statusCode)
		}
		if res.body != "slow-response-ok" {
			t.Errorf("expected body 'slow-response-ok', got %q", res.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for in-flight request to finish")
	}

	// 等待服务器正常退出
	select {
	case err := <-serverErrCh:
		if err != nil {
			t.Errorf("expected clean server shutdown, got error: %v", err)
		}
	case <-time.After(gracefulTimeout + 1*time.Second):
		t.Fatalf("server did not shut down within graceful_shutdown_timeout + 1s")
	}

	shutdownDuration := time.Since(shutdownStart)
	if shutdownDuration > gracefulTimeout+1*time.Second {
		t.Errorf("shutdown took too long: %v (limit: %v)", shutdownDuration, gracefulTimeout+1*time.Second)
	}
}

// TestGracefulShutdown_ForceCloseOnTimeout 验证当在途请求超过 graceful_shutdown_timeout 时强制关闭
func TestGracefulShutdown_ForceCloseOnTimeout(t *testing.T) {
	port := getFreePort(t)
	// 短超时，以促成强制关闭
	shortGracefulTimeout := 200 * time.Millisecond

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:                    port,
			ReadTimeout:             5 * time.Second,
			WriteTimeout:            5 * time.Second,
			GracefulShutdownTimeout: shortGracefulTimeout,
		},
	}

	handlerEntered := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(handlerEntered)
			// 耗时远大于 graceful_shutdown_timeout
			time.Sleep(1 * time.Second)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	srv := newHTTPServer(cfg, handler)
	pm := plugin.NewManager()
	gs := NewGracefulServer(srv, pm, nil, shortGracefulTimeout)

	go func() {
		_ = srv.ListenAndServe()
	}()
	waitForServerReady(t, port)

	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		_, _ = client.Get(fmt.Sprintf("http://127.0.0.1:%d/slow", port))
	}()

	<-handlerEntered

	start := time.Now()
	// 执行优雅关闭，应当超时并强制 Close()，返回错误
	err := gs.Shutdown(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Errorf("expected shutdown timeout error, got nil")
	}
	if elapsed > shortGracefulTimeout+500*time.Millisecond {
		t.Errorf("shutdown took too long to force close: %v", elapsed)
	}
}

// TestPortOccupied 验证端口被占用时 Listen 失败通过 errCh 返回错误，不崩溃
func TestPortOccupied(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	occupiedPort := ln.Addr().(*net.TCPAddr).Port

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:                    occupiedPort,
			GracefulShutdownTimeout: 1 * time.Second,
		},
	}

	srv := newHTTPServer(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	pm := plugin.NewManager()

	err = runServer(context.Background(), srv, cfg, pm, nil)
	if err == nil {
		t.Fatal("expected error when port is already occupied, got nil")
	}
}

// TestPIDFilePath 验证 PID 文件路径解析与读写
func TestPIDFilePath(t *testing.T) {
	t.Run("ConfiguredPIDFile", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.ServerConfig{
				Port:    8080,
				PIDFile: "/custom/run/gateway.pid",
			},
		}
		path := getPIDFilePath(cfg)
		if path != "/custom/run/gateway.pid" {
			t.Errorf("expected '/custom/run/gateway.pid', got %q", path)
		}
	})

	t.Run("DefaultWithPort", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.ServerConfig{
				Port: 9090,
			},
		}
		path := getPIDFilePath(cfg)
		expected := "/tmp/gateway-go-9090.pid"
		if path != expected {
			t.Errorf("expected %q, got %q", expected, path)
		}
	})

	t.Run("FallbackDefaultWithoutPort", func(t *testing.T) {
		path := getPIDFilePath(nil)
		if path != PIDFile {
			t.Errorf("expected %q, got %q", PIDFile, path)
		}

		cfgZeroPort := &config.Config{
			Server: config.ServerConfig{
				Port: 0,
			},
		}
		pathZero := getPIDFilePath(cfgZeroPort)
		if pathZero != PIDFile {
			t.Errorf("expected %q, got %q", PIDFile, pathZero)
		}
	})

	t.Run("WriteAndReadPIDFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		testPIDPath := filepath.Join(tmpDir, "test.pid")

		if err := writePIDFile(testPIDPath); err != nil {
			t.Fatalf("failed to write PID file: %v", err)
		}

		pid, err := readPIDFile(testPIDPath)
		if err != nil {
			t.Fatalf("failed to read PID file: %v", err)
		}

		if pid != os.Getpid() {
			t.Errorf("expected PID %d, got %d", os.Getpid(), pid)
		}
	})
}

// 辅助方法：获取空闲端口
func getFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// 辅助方法：等待服务就绪
func waitForServerReady(t *testing.T, port int) {
	t.Helper()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	url := fmt.Sprintf("http://127.0.0.1:%d/health-check-probe", port)
	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server on port %d failed to become ready within deadline", port)
}
