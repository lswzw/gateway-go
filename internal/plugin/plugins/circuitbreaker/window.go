package circuitbreaker

import (
	"sync/atomic"
	"time"
)

// Window 滑动窗口
type Window struct {
	buckets    []*Bucket
	size       int
	windowSize time.Duration
	current    int32
}

// Bucket 时间桶
type Bucket struct {
	failures  int32
	successes int32
	timestamp int64
}

// NewWindow 创建滑动窗口
func NewWindow(size int, windowSize time.Duration) *Window {
	w := &Window{
		buckets:    make([]*Bucket, size),
		size:       size,
		windowSize: windowSize,
	}

	// 初始化桶
	for i := 0; i < size; i++ {
		w.buckets[i] = &Bucket{
			timestamp: time.Now().UnixNano(),
		}
	}

	return w
}

// RecordFailure 记录失败
func (w *Window) RecordFailure() {
	w.getCurrentBucket().failures++
}

// RecordSuccess 记录成功
func (w *Window) RecordSuccess() {
	w.getCurrentBucket().successes++
}

// GetFailureRate 获取失败率
func (w *Window) GetFailureRate() float64 {
	var totalFailures, totalRequests int32

	now := time.Now().UnixNano()
	windowStart := now - w.windowSize.Nanoseconds()

	for _, bucket := range w.buckets {
		if bucket.timestamp < windowStart {
			continue
		}

		failures := atomic.LoadInt32(&bucket.failures)
		successes := atomic.LoadInt32(&bucket.successes)

		totalFailures += failures
		totalRequests += failures + successes
	}

	if totalRequests == 0 {
		return 0
	}

	return float64(totalFailures) / float64(totalRequests)
}

// getCurrentBucket 获取当前时间桶
func (w *Window) getCurrentBucket() *Bucket {
	now := time.Now().UnixNano()
	current := atomic.LoadInt32(&w.current)
	bucket := w.buckets[current]

	// 检查是否需要切换到新的桶
	if now-bucket.timestamp >= w.windowSize.Nanoseconds()/int64(w.size) {
		// 尝试切换到下一个桶
		next := (current + 1) % int32(w.size)
		if atomic.CompareAndSwapInt32(&w.current, current, next) {
			// 重置新桶的计数
			w.buckets[next] = &Bucket{
				timestamp: now,
			}
			return w.buckets[next]
		}
	}

	return bucket
}
