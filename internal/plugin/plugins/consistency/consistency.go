package consistency

import (
	"bufio"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gateway-go/internal/plugin/core"
	"gateway-go/internal/response"

	"github.com/gin-gonic/gin"
)

// Algorithm 定义支持的校验算法
type Algorithm string

const (
	AlgorithmHMACSHA256 Algorithm = "hmac-sha256"
	AlgorithmMD5        Algorithm = "md5"
	AlgorithmRSA        Algorithm = "rsa"
	AlgorithmECDSA      Algorithm = "ecdsa"
	AlgorithmEd25519    Algorithm = "ed25519"
)

// ConsistencyPlugin 一致性校验插件
type ConsistencyPlugin struct {
	*core.BasePlugin
	config      *Config
	usedNonces  map[string]time.Time
	nonceMu     sync.RWMutex
	stopCleanup chan struct{}
}

// Config 插件配置
type Config struct {
	// 是否启用插件
	Enabled bool `yaml:"enabled" json:"enabled"`
	// 校验算法
	Algorithm Algorithm `yaml:"algorithm" json:"algorithm"`
	// 密钥 (用于 HMAC)
	Secret string `yaml:"secret" json:"secret"`
	// 公钥 (用于 RSA、ECDSA、Ed25519)
	PublicKey string `yaml:"public_key" json:"public_key"`
	// 需要校验的字段
	Fields []string `yaml:"fields" json:"fields"`
	// 签名字段名
	SignatureField string `yaml:"signature_field" json:"signature_field"`
	// 是否校验响应
	CheckResponse bool `yaml:"check_response" json:"check_response"`
	// 时间戳有效期（秒）
	TimestampValidity int64 `yaml:"timestamp_validity" json:"timestamp_validity"`
	// Nonce 容量上限（默认 10000）
	MaxNonces int `yaml:"max_nonces" json:"max_nonces"`
}

// New 创建一致性校验插件实例
func New() *ConsistencyPlugin {
	p := &ConsistencyPlugin{
		BasePlugin: core.NewBasePlugin("consistency", 15, nil),
		config: &Config{
			Enabled:           true,
			Algorithm:         AlgorithmHMACSHA256,
			SignatureField:    "X-Signature",
			Fields:            []string{"timestamp", "nonce"},
			CheckResponse:     false,
			TimestampValidity: 300,   // 默认 5 分钟
			MaxNonces:         10000, // 默认最大 10000 个 nonce
		},
		usedNonces: make(map[string]time.Time),
	}
	p.startCleanupTicker()
	return p
}

// Init 初始化插件 (幂等且可重入 REQ-P1-02)
func (p *ConsistencyPlugin) Init(config interface{}) error {
	_ = p.Stop()

	if config == nil {
		return nil
	}

	// 类型断言
	configMap, ok := config.(map[string]interface{})
	if !ok {
		return fmt.Errorf("配置类型错误，期望 map[string]interface{}")
	}

	// 解析配置
	if enabled, ok := configMap["enabled"].(bool); ok {
		p.config.Enabled = enabled
	}
	if algorithm, ok := configMap["algorithm"].(string); ok {
		p.config.Algorithm = Algorithm(algorithm)
	}
	if secret, ok := configMap["secret"].(string); ok {
		p.config.Secret = secret
	}
	if publicKey, ok := configMap["public_key"].(string); ok {
		p.config.PublicKey = publicKey
	}
	if fields, ok := configMap["fields"].([]interface{}); ok {
		p.config.Fields = make([]string, len(fields))
		for i, field := range fields {
			p.config.Fields[i] = fmt.Sprintf("%v", field)
		}
	} else if fields, ok := configMap["fields"].([]string); ok {
		p.config.Fields = fields
	}
	if signatureField, ok := configMap["signature_field"].(string); ok {
		p.config.SignatureField = signatureField
	}
	if checkResponse, ok := configMap["check_response"].(bool); ok {
		p.config.CheckResponse = checkResponse
	}
	if tv, ok := configMap["timestamp_validity"]; ok {
		switch v := tv.(type) {
		case int64:
			p.config.TimestampValidity = v
		case int:
			p.config.TimestampValidity = int64(v)
		case float64:
			p.config.TimestampValidity = int64(v)
		}
	}
	if mn, ok := configMap["max_nonces"]; ok {
		switch v := mn.(type) {
		case int:
			p.config.MaxNonces = v
		case int64:
			p.config.MaxNonces = int(v)
		case float64:
			p.config.MaxNonces = int(v)
		}
	}

	if p.config.MaxNonces <= 0 {
		p.config.MaxNonces = 10000
	}

	p.startCleanupTicker()
	return nil
}

// startCleanupTicker 启动定期清理过期 nonce 的后台任务
func (p *ConsistencyPlugin) startCleanupTicker() {
	p.nonceMu.Lock()
	defer p.nonceMu.Unlock()

	if p.stopCleanup != nil {
		return
	}
	stopChan := make(chan struct{})
	p.stopCleanup = stopChan
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.cleanupExpiredNonces()
			case <-stopChan:
				return
			}
		}
	}()
}

// Stop 停止插件并释放资源
func (p *ConsistencyPlugin) Stop() error {
	p.nonceMu.Lock()
	defer p.nonceMu.Unlock()

	if p.stopCleanup != nil {
		close(p.stopCleanup)
		p.stopCleanup = nil
	}
	p.usedNonces = make(map[string]time.Time)
	return nil
}

// Execute 执行插件
func (p *ConsistencyPlugin) Execute(c *gin.Context) error {
	if !p.config.Enabled {
		return nil
	}

	// 校验请求
	statusCode, err := p.checkRequest(c)
	if err != nil {
		response.RespondError(c, statusCode, err.Error(), response.GetRequestID(c))
		return nil
	}

	// 如果需要校验响应
	if p.config.CheckResponse {
		c.Writer = &responseWriter{
			ResponseWriter: c.Writer,
			body:           []byte{},
			plugin:         p,
			context:        c,
		}
	}

	return nil
}

// checkRequest 校验请求，返回状态码与错误
func (p *ConsistencyPlugin) checkRequest(c *gin.Context) (int, error) {
	// MD5 不安全，默认禁止
	if p.config.Algorithm == AlgorithmMD5 {
		return http.StatusBadRequest, fmt.Errorf("insecure algorithm: md5 is disallowed")
	}

	// 获取签名
	signature := c.GetHeader(p.config.SignatureField)
	if signature == "" {
		return http.StatusBadRequest, fmt.Errorf("missing signature")
	}

	// 获取时间戳
	timestamp := c.GetHeader("timestamp")
	if timestamp == "" {
		return http.StatusBadRequest, fmt.Errorf("missing timestamp")
	}

	// 验证时间戳
	if code, err := p.validateTimestamp(timestamp); err != nil {
		return code, err
	}

	// 获取 nonce
	nonce := c.GetHeader("nonce")
	if nonce == "" {
		return http.StatusBadRequest, fmt.Errorf("missing nonce")
	}

	// 验证 nonce 是否已使用（原子检查并预记录）
	if code, err := p.validateAndRecordNonce(nonce); err != nil {
		return code, err
	}

	// 获取需要校验的字段值
	values := make([]string, 0, len(p.config.Fields))
	for _, field := range p.config.Fields {
		value := c.GetHeader(field)
		if value == "" {
			p.deleteNonce(nonce)
			return http.StatusBadRequest, fmt.Errorf("missing required field: %s", field)
		}
		values = append(values, value)
	}

	// 拼接待验签内容
	content := strings.Join(values, "&")

	// 验证签名
	if err := p.verifySignature(content, signature); err != nil {
		p.deleteNonce(nonce)
		return http.StatusUnauthorized, fmt.Errorf("invalid signature")
	}

	return 0, nil
}

// validateTimestamp 验证时间戳
func (p *ConsistencyPlugin) validateTimestamp(timestampStr string) (int, error) {
	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return http.StatusBadRequest, fmt.Errorf("invalid timestamp format")
	}

	// 兼容毫秒级时间戳 (>= 13 位)
	if timestamp > 1e11 {
		timestamp = timestamp / 1000
	}

	now := time.Now().Unix()
	if math.Abs(float64(now-timestamp)) > float64(p.config.TimestampValidity) {
		return http.StatusUnauthorized, fmt.Errorf("timestamp expired")
	}

	return 0, nil
}

// validateAndRecordNonce 验证并记录 nonce
func (p *ConsistencyPlugin) validateAndRecordNonce(nonce string) (int, error) {
	p.nonceMu.Lock()
	defer p.nonceMu.Unlock()

	now := time.Now()
	validity := time.Duration(p.config.TimestampValidity) * time.Second

	// 若 nonce 已存在且未过期，判定为重放攻击
	if t, exists := p.usedNonces[nonce]; exists {
		if now.Sub(t) <= validity {
			return http.StatusUnauthorized, fmt.Errorf("nonce 已使用")
		}
	}

	// 检查容量限制，若达到上限先做一次过期清理
	if len(p.usedNonces) >= p.config.MaxNonces {
		for k, t := range p.usedNonces {
			if now.Sub(t) > validity {
				delete(p.usedNonces, k)
			}
		}
	}

	// 清理后若仍达上限，拒绝新增并返回错误
	if len(p.usedNonces) >= p.config.MaxNonces {
		return http.StatusTooManyRequests, fmt.Errorf("nonce storage capacity reached")
	}

	// 记录 nonce
	p.usedNonces[nonce] = now
	return 0, nil
}

// deleteNonce 回滚未验签通过的 nonce
func (p *ConsistencyPlugin) deleteNonce(nonce string) {
	p.nonceMu.Lock()
	defer p.nonceMu.Unlock()
	delete(p.usedNonces, nonce)
}

// cleanupExpiredNonces 清理过期的 nonce
func (p *ConsistencyPlugin) cleanupExpiredNonces() {
	p.nonceMu.Lock()
	defer p.nonceMu.Unlock()

	now := time.Now()
	validity := time.Duration(p.config.TimestampValidity) * time.Second
	for key, value := range p.usedNonces {
		if now.Sub(value) > validity {
			delete(p.usedNonces, key)
		}
	}
}

// verifySignature 校验签名
func (p *ConsistencyPlugin) verifySignature(content string, sigStr string) error {
	switch p.config.Algorithm {
	case AlgorithmHMACSHA256, "":
		return p.verifyHMAC(content, sigStr)
	case AlgorithmRSA:
		return p.verifyRSA(content, sigStr)
	case AlgorithmECDSA:
		return p.verifyECDSA(content, sigStr)
	case AlgorithmEd25519:
		return p.verifyEd25519(content, sigStr)
	case AlgorithmMD5:
		return fmt.Errorf("insecure algorithm: md5 is disallowed")
	default:
		return fmt.Errorf("unsupported algorithm: %s", p.config.Algorithm)
	}
}

// verifyHMAC 使用常量时间比较校验 HMAC-SHA256 签名，防御时序攻击
func (p *ConsistencyPlugin) verifyHMAC(content string, sigStr string) error {
	h := hmac.New(sha256.New, []byte(p.config.Secret))
	h.Write([]byte(content))
	expectedMAC := h.Sum(nil)
	expectedHex := hex.EncodeToString(expectedMAC)

	// 支持客户端传入十六进制签名
	if sigBytes, err := hex.DecodeString(sigStr); err == nil && len(sigBytes) > 0 {
		if hmac.Equal(expectedMAC, sigBytes) {
			return nil
		}
	}

	// 支持客户端传入十六进制字符串，使用 hmac.Equal 比较字节序列
	if hmac.Equal([]byte(strings.ToLower(expectedHex)), []byte(strings.ToLower(sigStr))) {
		return nil
	}

	return fmt.Errorf("invalid signature")
}

// decodeSignatureBytes 解码十六进制或 Base64 格式的签名
func decodeSignatureBytes(sigStr string) ([]byte, error) {
	sigStr = strings.TrimSpace(sigStr)
	// 尝试十六进制解码
	if b, err := hex.DecodeString(sigStr); err == nil && len(b) > 0 {
		return b, nil
	}
	// 尝试标准 Base64 解码
	if b, err := base64.StdEncoding.DecodeString(sigStr); err == nil && len(b) > 0 {
		return b, nil
	}
	// 尝试 URL Safe Base64 解码
	if b, err := base64.URLEncoding.DecodeString(sigStr); err == nil && len(b) > 0 {
		return b, nil
	}
	return nil, fmt.Errorf("signature must be hex or base64 encoded")
}

// parsePublicKeyPEM 解析公钥 PEM 块为 DER 字节
func parsePublicKeyPEM(keyPEM string) ([]byte, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block != nil {
		return block.Bytes, nil
	}
	// 尝试直接作为 Base64 DER 解析
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyPEM))
	if err == nil && len(b) > 0 {
		return b, nil
	}
	return nil, fmt.Errorf("failed to parse public key PEM")
}

// verifyRSA 校验客户端 RSA 签名（真正使用公钥验证，防御伪造）
func (p *ConsistencyPlugin) verifyRSA(content string, sigStr string) error {
	if p.config.PublicKey == "" {
		return fmt.Errorf("RSA public key not configured")
	}

	derBytes, err := parsePublicKeyPEM(p.config.PublicKey)
	if err != nil {
		return err
	}

	pub, err := x509.ParsePKIXPublicKey(derBytes)
	if err != nil {
		// 尝试 PKCS#1 格式
		pub, err = x509.ParsePKCS1PublicKey(derBytes)
		if err != nil {
			return fmt.Errorf("failed to parse RSA public key: %v", err)
		}
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("not an RSA public key")
	}

	sigBytes, err := decodeSignatureBytes(sigStr)
	if err != nil {
		return fmt.Errorf("invalid signature format: %v", err)
	}

	hashed := sha256.Sum256([]byte(content))

	// 优先以 PKCS#1 v1.5 格式验签
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, hashed[:], sigBytes); err == nil {
		return nil
	}

	// 兼容以 PSS 格式验签
	if err := rsa.VerifyPSS(rsaPub, crypto.SHA256, hashed[:], sigBytes, nil); err == nil {
		return nil
	}

	return fmt.Errorf("invalid signature")
}

// verifyECDSA 校验客户端 ECDSA 签名
func (p *ConsistencyPlugin) verifyECDSA(content string, sigStr string) error {
	if p.config.PublicKey == "" {
		return fmt.Errorf("ECDSA public key not configured")
	}

	derBytes, err := parsePublicKeyPEM(p.config.PublicKey)
	if err != nil {
		return err
	}

	pub, err := x509.ParsePKIXPublicKey(derBytes)
	if err != nil {
		return fmt.Errorf("failed to parse ECDSA public key: %v", err)
	}

	ecdsaPub, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("not an ECDSA public key")
	}

	sigBytes, err := decodeSignatureBytes(sigStr)
	if err != nil {
		return fmt.Errorf("invalid signature format: %v", err)
	}

	hashed := sha256.Sum256([]byte(content))

	// 尝试 ASN.1 DER 验签
	if ecdsa.VerifyASN1(ecdsaPub, hashed[:], sigBytes) {
		return nil
	}

	// 尝试 raw (r, s) 验签
	if len(sigBytes) > 0 && len(sigBytes)%2 == 0 {
		half := len(sigBytes) / 2
		r := new(big.Int).SetBytes(sigBytes[:half])
		s := new(big.Int).SetBytes(sigBytes[half:])
		if ecdsa.Verify(ecdsaPub, hashed[:], r, s) {
			return nil
		}
	}

	return fmt.Errorf("invalid signature")
}

// verifyEd25519 校验客户端 Ed25519 签名
func (p *ConsistencyPlugin) verifyEd25519(content string, sigStr string) error {
	if p.config.PublicKey == "" {
		return fmt.Errorf("Ed25519 public key not configured")
	}

	var edPub ed25519.PublicKey
	derBytes, err := parsePublicKeyPEM(p.config.PublicKey)
	if err == nil {
		pub, parseErr := x509.ParsePKIXPublicKey(derBytes)
		if parseErr == nil {
			var ok bool
			edPub, ok = pub.(ed25519.PublicKey)
			if !ok {
				return fmt.Errorf("not an Ed25519 public key")
			}
		}
	}

	if edPub == nil {
		// 尝试直接作为 32 字节原始公钥 (hex 或 base64)
		cleanKey := strings.TrimSpace(p.config.PublicKey)
		if b, err := hex.DecodeString(cleanKey); err == nil && len(b) == ed25519.PublicKeySize {
			edPub = b
		} else if b, err := base64.StdEncoding.DecodeString(cleanKey); err == nil && len(b) == ed25519.PublicKeySize {
			edPub = b
		} else {
			return fmt.Errorf("failed to parse Ed25519 public key")
		}
	}

	sigBytes, err := decodeSignatureBytes(sigStr)
	if err != nil {
		return fmt.Errorf("invalid signature format: %v", err)
	}

	if !ed25519.Verify(edPub, []byte(content), sigBytes) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// responseWriter 响应写入器
type responseWriter struct {
	gin.ResponseWriter
	body    []byte
	plugin  *ConsistencyPlugin
	context *gin.Context
}

// Write 写入响应
func (w *responseWriter) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return w.ResponseWriter.Write(b)
}

// WriteString 写入字符串响应
func (w *responseWriter) WriteString(s string) (int, error) {
	w.body = append(w.body, []byte(s)...)
	return w.ResponseWriter.WriteString(s)
}

// WriteHeader 写入响应头
func (w *responseWriter) WriteHeader(code int) {
	// 如果响应成功，校验响应内容
	if code == http.StatusOK {
		signature := w.Header().Get(w.plugin.config.SignatureField)
		if signature != "" {
			// 校验响应签名
			err := w.plugin.verifySignature(string(w.body), signature)
			if err != nil {
				w.ResponseWriter.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
	}

	w.ResponseWriter.WriteHeader(code)
}

// Close 关闭响应写入器
func (w *responseWriter) Close() error {
	return nil
}

// Flush 刷新响应写入器
func (w *responseWriter) Flush() {
	w.ResponseWriter.Flush()
}

// Hijack 实现 http.Hijacker 接口
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}

// Pusher 实现 http.Pusher 接口
func (w *responseWriter) Pusher() http.Pusher {
	return w.ResponseWriter.(http.Pusher)
}
