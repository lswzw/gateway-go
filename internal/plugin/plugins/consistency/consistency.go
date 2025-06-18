package consistency

import (
	"bufio"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gateway-go/internal/plugin/core"

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
	config *Config
	// 用于存储已使用的 nonce
	usedNonces sync.Map
}

// Config 插件配置
type Config struct {
	// 是否启用插件
	Enabled bool `yaml:"enabled"`
	// 校验算法
	Algorithm Algorithm `yaml:"algorithm"`
	// 密钥
	Secret string `yaml:"secret"`
	// 公钥（用于 RSA、ECDSA、Ed25519）
	PublicKey string `yaml:"public_key"`
	// 需要校验的字段
	Fields []string `yaml:"fields"`
	// 签名字段名
	SignatureField string `yaml:"signature_field"`
	// 是否校验响应
	CheckResponse bool `yaml:"check_response"`
	// 时间戳有效期（秒）
	TimestampValidity int64 `yaml:"timestamp_validity"`
}

// New 创建一致性校验插件实例
func New() *ConsistencyPlugin {
	return &ConsistencyPlugin{
		BasePlugin: core.NewBasePlugin("consistency", 15, nil),
		config: &Config{
			Enabled:           true,
			Algorithm:         AlgorithmHMACSHA256,
			SignatureField:    "X-Signature",
			Fields:            []string{"timestamp", "nonce"},
			CheckResponse:     false,
			TimestampValidity: 300, // 默认 5 分钟
		},
	}
}

// Init 初始化插件
func (p *ConsistencyPlugin) Init(config interface{}) error {
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
			p.config.Fields[i] = field.(string)
		}
	}
	if signatureField, ok := configMap["signature_field"].(string); ok {
		p.config.SignatureField = signatureField
	}
	if checkResponse, ok := configMap["check_response"].(bool); ok {
		p.config.CheckResponse = checkResponse
	}
	if timestampValidity, ok := configMap["timestamp_validity"].(int64); ok {
		p.config.TimestampValidity = timestampValidity
	}

	return nil
}

// Execute 执行插件
func (p *ConsistencyPlugin) Execute(c *gin.Context) error {
	if !p.config.Enabled {
		return nil
	}

	// 校验请求
	if err := p.checkRequest(c); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		c.Abort()
		return err
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

// checkRequest 校验请求
func (p *ConsistencyPlugin) checkRequest(c *gin.Context) error {
	// 获取签名
	signature := c.GetHeader(p.config.SignatureField)
	if signature == "" {
		return fmt.Errorf("missing signature")
	}

	// 获取时间戳
	timestamp := c.GetHeader("timestamp")
	if timestamp == "" {
		return fmt.Errorf("missing timestamp")
	}

	// 验证时间戳
	if err := p.validateTimestamp(timestamp); err != nil {
		return err
	}

	// 获取 nonce
	nonce := c.GetHeader("nonce")
	if nonce == "" {
		return fmt.Errorf("missing nonce")
	}

	// 验证 nonce 是否已使用
	if !p.validateNonce(nonce) {
		return fmt.Errorf("nonce 已使用")
	}

	// 获取需要校验的字段值
	values := make([]string, 0, len(p.config.Fields))
	for _, field := range p.config.Fields {
		value := c.GetHeader(field)
		if value == "" {
			return fmt.Errorf("missing required field: %s", field)
		}
		values = append(values, value)
	}

	// 计算签名
	calculatedSignature, err := p.calculateSignature(values)
	if err != nil {
		return err
	}

	// 比较签名
	if calculatedSignature != signature {
		return fmt.Errorf("invalid signature")
	}

	// 记录已使用的 nonce
	p.usedNonces.Store(nonce, time.Now())

	return nil
}

// validateTimestamp 验证时间戳
func (p *ConsistencyPlugin) validateTimestamp(timestampStr string) error {
	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp format")
	}

	now := time.Now().Unix()
	if math.Abs(float64(now-timestamp)) > float64(p.config.TimestampValidity) {
		return fmt.Errorf("timestamp expired")
	}

	return nil
}

// validateNonce 验证 nonce 是否已使用
func (p *ConsistencyPlugin) validateNonce(nonce string) bool {
	// 检查 nonce 是否已使用
	if _, exists := p.usedNonces.Load(nonce); exists {
		return false
	}

	// 清理过期的 nonce
	p.cleanupExpiredNonces()

	return true
}

// cleanupExpiredNonces 清理过期的 nonce
func (p *ConsistencyPlugin) cleanupExpiredNonces() {
	now := time.Now()
	p.usedNonces.Range(func(key, value interface{}) bool {
		if timestamp, ok := value.(time.Time); ok {
			if now.Sub(timestamp) > time.Duration(p.config.TimestampValidity)*time.Second {
				p.usedNonces.Delete(key)
			}
		}
		return true
	})
}

// calculateSignature 计算签名
func (p *ConsistencyPlugin) calculateSignature(values []string) (string, error) {
	// 拼接字段值
	content := strings.Join(values, "&")

	switch p.config.Algorithm {
	case AlgorithmHMACSHA256:
		h := hmac.New(sha256.New, []byte(p.config.Secret))
		h.Write([]byte(content))
		return hex.EncodeToString(h.Sum(nil)), nil
	case AlgorithmMD5:
		h := md5.New()
		h.Write([]byte(content))
		return hex.EncodeToString(h.Sum(nil)), nil
	case AlgorithmRSA:
		return p.calculateRSASignature(content)
	case AlgorithmECDSA:
		return p.calculateECDSASignature(content)
	case AlgorithmEd25519:
		return p.calculateEd25519Signature(content)
	default:
		return "", fmt.Errorf("unsupported algorithm: %s", p.config.Algorithm)
	}
}

// calculateRSASignature 计算 RSA 签名
func (p *ConsistencyPlugin) calculateRSASignature(content string) (string, error) {
	if p.config.PublicKey == "" {
		return "", fmt.Errorf("RSA public key not configured")
	}

	// 解析公钥
	block, _ := pem.Decode([]byte(p.config.PublicKey))
	if block == nil {
		return "", fmt.Errorf("failed to parse RSA public key")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse RSA public key: %v", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("not an RSA public key")
	}

	// 计算签名
	h := sha256.New()
	h.Write([]byte(content))
	hashed := h.Sum(nil)

	// 验证签名
	err = rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, hashed, []byte(content))
	if err != nil {
		return "", fmt.Errorf("failed to verify RSA signature: %v", err)
	}

	return hex.EncodeToString(hashed), nil
}

// calculateECDSASignature 计算 ECDSA 签名
func (p *ConsistencyPlugin) calculateECDSASignature(content string) (string, error) {
	if p.config.PublicKey == "" {
		return "", fmt.Errorf("ECDSA public key not configured")
	}

	// 解析公钥
	block, _ := pem.Decode([]byte(p.config.PublicKey))
	if block == nil {
		return "", fmt.Errorf("failed to parse ECDSA public key")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse ECDSA public key: %v", err)
	}

	_, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("not an ECDSA public key")
	}

	// 计算签名
	h := sha256.New()
	h.Write([]byte(content))
	hashed := h.Sum(nil)

	// 由于我们只有公钥，这里只能验证签名
	// 实际签名应该在客户端使用私钥完成
	return hex.EncodeToString(hashed), nil
}

// calculateEd25519Signature 计算 Ed25519 签名
func (p *ConsistencyPlugin) calculateEd25519Signature(content string) (string, error) {
	if p.config.PublicKey == "" {
		return "", fmt.Errorf("Ed25519 public key not configured")
	}

	// 解析公钥
	block, _ := pem.Decode([]byte(p.config.PublicKey))
	if block == nil {
		return "", fmt.Errorf("failed to parse Ed25519 public key")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse Ed25519 public key: %v", err)
	}

	_, ok := pub.(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("not an Ed25519 public key")
	}

	// 计算签名
	// 由于我们只有公钥，这里只能验证签名
	// 实际签名应该在客户端使用私钥完成
	h := sha256.New()
	h.Write([]byte(content))
	hashed := h.Sum(nil)
	return hex.EncodeToString(hashed), nil
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
		// 获取响应签名
		signature := w.Header().Get(w.plugin.config.SignatureField)
		if signature != "" {
			// 计算响应签名
			calculatedSignature, err := w.plugin.calculateSignature([]string{string(w.body)})
			if err != nil {
				w.ResponseWriter.WriteHeader(http.StatusInternalServerError)
				return
			}

			// 比较签名
			if calculatedSignature != signature {
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
