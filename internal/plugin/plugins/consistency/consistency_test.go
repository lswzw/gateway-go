package consistency

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTestContext(method, path string, headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c, w
}

// TestVerifyHMAC 测试 HMAC-SHA256 签名校验（有效签名通过，无效签名返回 401）
func TestVerifyHMAC(t *testing.T) {
	plugin := New()
	defer plugin.Stop()

	secret := "test-secret-key-12345"
	err := plugin.Init(map[string]interface{}{
		"enabled":            true,
		"algorithm":          "hmac-sha256",
		"secret":             secret,
		"fields":             []interface{}{"timestamp", "nonce", "X-Custom"},
		"signature_field":    "X-Signature",
		"timestamp_validity": int64(300),
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "nonce-hmac-001"
	custom := "custom-value"
	content := fmt.Sprintf("%s&%s&%s", ts, nonce, custom)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(content))
	validSig := hex.EncodeToString(mac.Sum(nil))

	// 1. 有效 HMAC 签名
	c, w := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       nonce,
		"X-Custom":    custom,
		"X-Signature": validSig,
	})
	if err := plugin.Execute(c); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	// 2. 无效/篡改的 HMAC 签名
	c2, w2 := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       "nonce-hmac-002",
		"X-Custom":    custom,
		"X-Signature": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err := plugin.Execute(c2); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid HMAC signature, got %d, body: %s", w2.Code, w2.Body.String())
	}
}

// TestVerifyRSA 测试真实 RSA 签名校验（有效签名通过，伪造签名返回 401）
func TestVerifyRSA(t *testing.T) {
	plugin := New()
	defer plugin.Stop()

	// 生成真实 RSA 密钥对
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey failed: %v", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	}))

	err = plugin.Init(map[string]interface{}{
		"enabled":            true,
		"algorithm":          "rsa",
		"public_key":         pubPEM,
		"fields":             []interface{}{"timestamp", "nonce"},
		"signature_field":    "X-Signature",
		"timestamp_validity": int64(300),
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "nonce-rsa-001"
	content := fmt.Sprintf("%s&%s", ts, nonce)

	// 使用私钥签名
	hashed := sha256.Sum256([]byte(content))
	sigBytes, err := rsa.SignPKCS1v15(rand.Reader, privKey, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15 failed: %v", err)
	}
	validSig := hex.EncodeToString(sigBytes)

	// 1. 有效 RSA 签名放行
	c, w := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       nonce,
		"X-Signature": validSig,
	})
	if err := plugin.Execute(c); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid RSA signature, got %d, body: %s", w.Code, w.Body.String())
	}

	// 2. 伪造/篡改的 RSA 签名被拒 401
	forgedSigBytes := make([]byte, len(sigBytes))
	copy(forgedSigBytes, sigBytes)
	forgedSigBytes[0] ^= 0xFF // 篡改签名数据
	forgedSig := hex.EncodeToString(forgedSigBytes)

	c2, w2 := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       "nonce-rsa-002",
		"X-Signature": forgedSig,
	})
	if err := plugin.Execute(c2); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for forged RSA signature, got %d, body: %s", w2.Code, w2.Body.String())
	}
}

// TestNonceReplay 测试 Nonce 重放攻击防护（相同 nonce 第二次被拒）
func TestNonceReplay(t *testing.T) {
	plugin := New()
	defer plugin.Stop()

	secret := "secret-replay-test"
	err := plugin.Init(map[string]interface{}{
		"enabled":            true,
		"algorithm":          "hmac-sha256",
		"secret":             secret,
		"fields":             []interface{}{"timestamp", "nonce"},
		"signature_field":    "X-Signature",
		"timestamp_validity": int64(300),
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "unique-nonce-12345"
	content := fmt.Sprintf("%s&%s", ts, nonce)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(content))
	sig := hex.EncodeToString(mac.Sum(nil))

	// 第一次请求：应成功
	c1, w1 := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       nonce,
		"X-Signature": sig,
	})
	if err := plugin.Execute(c1); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if w1.Code != http.StatusOK {
		t.Fatalf("first request expected 200, got %d, body: %s", w1.Code, w1.Body.String())
	}

	// 第二次请求（相同的 nonce 重放）：应被拒（401）
	c2, w2 := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       nonce,
		"X-Signature": sig,
	})
	if err := plugin.Execute(c2); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("replay request expected 401, got %d, body: %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), "nonce 已使用") {
		t.Errorf("expected error message to contain 'nonce 已使用', got %s", w2.Body.String())
	}
}

// TestNonceCapacityLimit 测试 Nonce 容量限制
func TestNonceCapacityLimit(t *testing.T) {
	plugin := New()
	defer plugin.Stop()

	secret := "capacity-secret"
	err := plugin.Init(map[string]interface{}{
		"enabled":            true,
		"algorithm":          "hmac-sha256",
		"secret":             secret,
		"fields":             []interface{}{"timestamp", "nonce"},
		"timestamp_validity": int64(300),
		"max_nonces":         5,
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ts := strconv.FormatInt(time.Now().Unix(), 10)

	// 填满 5 个容量
	for i := 0; i < 5; i++ {
		nonce := fmt.Sprintf("nonce-cap-%d", i)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(fmt.Sprintf("%s&%s", ts, nonce)))
		sig := hex.EncodeToString(mac.Sum(nil))

		c, w := setupTestContext("GET", "/test", map[string]string{
			"timestamp":   ts,
			"nonce":       nonce,
			"X-Signature": sig,
		})
		if err := plugin.Execute(c); err != nil || w.Code != http.StatusOK {
			t.Fatalf("request %d failed: code %d, body %s", i, w.Code, w.Body.String())
		}
	}

	// 第 6 个请求超过容量限制，应被拒绝
	nonce6 := "nonce-cap-overflow"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%s&%s", ts, nonce6)))
	sig6 := hex.EncodeToString(mac.Sum(nil))

	c6, w6 := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       nonce6,
		"X-Signature": sig6,
	})
	if err := plugin.Execute(c6); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if w6.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 when nonce capacity exceeded, got %d, body: %s", w6.Code, w6.Body.String())
	}
}

// TestDisallowMD5Algorithm 测试 MD5 算法被明确禁止
func TestDisallowMD5Algorithm(t *testing.T) {
	plugin := New()
	defer plugin.Stop()

	err := plugin.Init(map[string]interface{}{
		"enabled":   true,
		"algorithm": "md5",
		"fields":    []interface{}{"timestamp", "nonce"},
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	c, w := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       "nonce-md5",
		"X-Signature": "some-md5-signature",
	})
	if err := plugin.Execute(c); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for md5 algorithm, got %d, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "md5 is disallowed") {
		t.Errorf("expected error message to contain 'md5 is disallowed', got %s", w.Body.String())
	}
}

// TestVerifyECDSA 测试 ECDSA 真实验签
func TestVerifyECDSA(t *testing.T) {
	plugin := New()
	defer plugin.Stop()

	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey failed: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey failed: %v", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	}))

	err = plugin.Init(map[string]interface{}{
		"enabled":            true,
		"algorithm":          "ecdsa",
		"public_key":         pubPEM,
		"fields":             []interface{}{"timestamp", "nonce"},
		"timestamp_validity": int64(300),
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "nonce-ecdsa-01"
	content := fmt.Sprintf("%s&%s", ts, nonce)
	hashed := sha256.Sum256([]byte(content))
	sigBytes, err := ecdsa.SignASN1(rand.Reader, privKey, hashed[:])
	if err != nil {
		t.Fatalf("SignASN1 failed: %v", err)
	}

	// 1. 有效 ECDSA 签名放行
	c, w := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       nonce,
		"X-Signature": hex.EncodeToString(sigBytes),
	})
	if err := plugin.Execute(c); err != nil || w.Code != http.StatusOK {
		t.Fatalf("valid ECDSA expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	// 2. 伪造 ECDSA 签名被拒 401
	c2, w2 := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       "nonce-ecdsa-02",
		"X-Signature": hex.EncodeToString([]byte("invalid-ecdsa-sig")),
	})
	if err := plugin.Execute(c2); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("forged ECDSA expected 401, got %d, body: %s", w2.Code, w2.Body.String())
	}
}

// TestVerifyEd25519 测试 Ed25519 真实验签
func TestVerifyEd25519(t *testing.T) {
	plugin := New()
	defer plugin.Stop()

	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey failed: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey failed: %v", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	}))

	err = plugin.Init(map[string]interface{}{
		"enabled":            true,
		"algorithm":          "ed25519",
		"public_key":         pubPEM,
		"fields":             []interface{}{"timestamp", "nonce"},
		"timestamp_validity": int64(300),
	})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "nonce-ed-01"
	content := fmt.Sprintf("%s&%s", ts, nonce)
	sigBytes := ed25519.Sign(privKey, []byte(content))

	// 1. 有效 Ed25519 签名放行
	c, w := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       nonce,
		"X-Signature": hex.EncodeToString(sigBytes),
	})
	if err := plugin.Execute(c); err != nil || w.Code != http.StatusOK {
		t.Fatalf("valid Ed25519 expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	// 2. 伪造 Ed25519 签名被拒 401
	c2, w2 := setupTestContext("GET", "/test", map[string]string{
		"timestamp":   ts,
		"nonce":       "nonce-ed-02",
		"X-Signature": hex.EncodeToString([]byte("invalid-ed25519-signature-bytes")),
	})
	if err := plugin.Execute(c2); err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("forged Ed25519 expected 401, got %d, body: %s", w2.Code, w2.Body.String())
	}
}
