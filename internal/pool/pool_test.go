package pool

import (
	"testing"
)

func TestBufferPool(t *testing.T) {
	bp := NewBufferPool()

	buf := bp.Get()
	if buf == nil {
		t.Fatalf("expected non-nil buffer")
	}

	buf.WriteString("hello world")
	if buf.String() != "hello world" {
		t.Fatalf("expected buffer content 'hello world', got %s", buf.String())
	}

	bp.Put(buf)

	// After put, buf was reset
	if buf.Len() != 0 {
		t.Errorf("expected buffer to be reset to len 0, got len %d", buf.Len())
	}

	// Get another buffer
	buf2 := bp.Get()
	if buf2.Len() != 0 {
		t.Errorf("expected clean buffer from pool")
	}
}

func TestRequestPool(t *testing.T) {
	rp := NewRequestPool()

	req := rp.Get()
	if req == nil {
		t.Fatalf("expected non-nil request")
	}

	req.Method = "POST"
	req.URL = "http://example.com/api"
	req.Headers["Authorization"] = "Bearer token123"
	req.Query["page"] = "1"
	req.Body = append(req.Body, []byte("request-data")...)

	rp.Put(req)

	if req.Method != "" || req.URL != "" || len(req.Body) != 0 || len(req.Headers) != 0 || len(req.Query) != 0 {
		t.Errorf("expected request fields to be reset after Put, got method=%s, url=%s, body=%v, headers=%v, query=%v",
			req.Method, req.URL, req.Body, req.Headers, req.Query)
	}

	// Verify reuse
	req2 := rp.Get()
	if len(req2.Headers) != 0 || len(req2.Query) != 0 {
		t.Errorf("expected clean maps in reused request")
	}
}

func BenchmarkBufferPool(b *testing.B) {
	bp := NewBufferPool()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := bp.Get()
		buf.Write([]byte("test data for benchmarking"))
		bp.Put(buf)
	}
}
