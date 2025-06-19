package pool

import (
	"bytes"
	"sync"
)

// BufferPool 缓冲区对象池
type BufferPool struct {
	pool sync.Pool
}

// NewBufferPool 创建缓冲区池
func NewBufferPool() *BufferPool {
	return &BufferPool{
		pool: sync.Pool{
			New: func() interface{} {
				return bytes.NewBuffer(make([]byte, 0, 1024))
			},
		},
	}
}

// Get 获取缓冲区
func (bp *BufferPool) Get() *bytes.Buffer {
	return bp.pool.Get().(*bytes.Buffer)
}

// Put 归还缓冲区
func (bp *BufferPool) Put(buf *bytes.Buffer) {
	buf.Reset()
	bp.pool.Put(buf)
}

// ProxyRequest 代理请求对象
type ProxyRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Query   map[string]string
	Body    []byte
}

// Reset 重置请求对象
func (pr *ProxyRequest) Reset() {
	pr.Method = ""
	pr.URL = ""
	pr.Body = pr.Body[:0]
	for k := range pr.Headers {
		delete(pr.Headers, k)
	}
	for k := range pr.Query {
		delete(pr.Query, k)
	}
}

// RequestPool 请求对象池
type RequestPool struct {
	pool sync.Pool
}

// NewRequestPool 创建请求对象池
func NewRequestPool() *RequestPool {
	return &RequestPool{
		pool: sync.Pool{
			New: func() interface{} {
				return &ProxyRequest{
					Headers: make(map[string]string),
					Query:   make(map[string]string),
				}
			},
		},
	}
}

// Get 获取请求对象
func (rp *RequestPool) Get() *ProxyRequest {
	return rp.pool.Get().(*ProxyRequest)
}

// Put 归还请求对象
func (rp *RequestPool) Put(req *ProxyRequest) {
	req.Reset()
	rp.pool.Put(req)
}
