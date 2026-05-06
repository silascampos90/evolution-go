package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var _k1 = []byte{0x59, 0xba, 0xa1, 0x12, 0xc1, 0xc8, 0x80, 0xec, 0x1e, 0xf5, 0xd7, 0x4f, 0xa5, 0xfe, 0x9b, 0xf9, 0x6d, 0x5e, 0x14, 0x69, 0x99, 0x89, 0xa6, 0xe4, 0xef, 0xc5, 0xdc, 0x60, 0xf5, 0x84, 0xcd, 0x06, 0x5b, 0x8a, 0x0d, 0x94, 0xa3, 0x94, 0xb7, 0x34, 0x2c, 0x25}
var _k0 = []byte{0x31, 0xce, 0xd5, 0x62, 0xb2, 0xf2, 0xaf, 0xc3, 0x72, 0x9c, 0xb4, 0x2a, 0xcb, 0x8d, 0xfe, 0xd7, 0x08, 0x28, 0x7b, 0x05, 0xec, 0xfd, 0xcf, 0x8b, 0x81, 0xa3, 0xb3, 0x15, 0x9b, 0xe0, 0xac, 0x72, 0x32, 0xe5, 0x63, 0xba, 0xc0, 0xfb, 0xda, 0x1a, 0x4e, 0x57}

var (
	_blq string
	_ik    string
)

func _it2l() string {
	if _blq != "" && _ik != "" {
		return _zh(_blq, _ik)
	}
	parts := [...]string{"h", "tt", "ps", "://", "li", "ce", "nse", ".", "ev", "ol", "ut", "io", "nf", "ou", "nd", "at", "io", "n.", "co", "m.", "br"}
	var s string
	for _, p := range parts {
		s += p
	}
	return s
}

func _zh(enc, key string) string {
	encBytes := _afc(enc)
	keyBytes := _afc(key)
	if len(keyBytes) == 0 {
		return ""
	}
	out := make([]byte, len(encBytes))
	for i, b := range encBytes {
		out[i] = b ^ keyBytes[i%len(keyBytes)]
	}
	return string(out)
}

func _afc(s string) []byte {
	if len(s)%2 != 0 {
		return nil
	}
	b := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		b[i/2] = _pl(s[i])<<4 | _pl(s[i+1])
	}
	return b
}

func _pl(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

var _j7g = &http.Client{Timeout: 10 * time.Second}

func _trei(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func _iv(path string, payload interface{}, _5i string) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := _it2l() + path
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", _5i)
	req.Header.Set("X-Signature", _trei(body, _5i))

	return _j7g.Do(req)
}

func _92(path string) (*http.Response, error) {
	url := _it2l() + path
	return _j7g.Get(url)
}

func _5lk(path string, payload interface{}) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := _it2l() + path
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return _j7g.Do(req)
}

func _r6(resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	var _zi0 struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(b, &_zi0); err == nil {
		msg := _zi0.Message
		if msg == "" {
			msg = _zi0.Error
		}
		if msg != "" {
			return fmt.Errorf("%s (HTTP %d)", strings.ToLower(msg), resp.StatusCode)
		}
	}
	return fmt.Errorf("HTTP %d", resp.StatusCode)
}

type RuntimeConfig struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Key        string    `gorm:"uniqueIndex;size:100;not null" json:"key"`
	Value      string    `gorm:"type:text;not null" json:"value"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (RuntimeConfig) TableName() string {
	return "runtime_configs"
}

const (
	ConfigKeyInstanceID = "instance_id"
	ConfigKeyAPIKey     = "api_key"
	ConfigKeyTier       = "tier"
	ConfigKeyCustomerID = "customer_id"
)

var _lme2 *gorm.DB

func SetDB(db *gorm.DB) {
	_lme2 = db
}

func MigrateDB() error {
	if _lme2 == nil {
		return fmt.Errorf("core: database not set, call SetDB first")
	}
	return _lme2.AutoMigrate(&RuntimeConfig{})
}

func _51(key string) (string, error) {
	if _lme2 == nil {
		return "", fmt.Errorf("core: database not set")
	}
	var _ax RuntimeConfig
	_9cn := _lme2.Where("key = ?", key).First(&_ax)
	if _9cn.Error != nil {
		return "", _9cn.Error
	}
	return _ax.Value, nil
}

func _wa(key, value string) error {
	if _lme2 == nil {
		return fmt.Errorf("core: database not set")
	}
	var _ax RuntimeConfig
	_9cn := _lme2.Where("key = ?", key).First(&_ax)
	if _9cn.Error != nil {
		return _lme2.Create(&RuntimeConfig{Key: key, Value: value}).Error
	}
	return _lme2.Model(&_ax).Update("value", value).Error
}

func _jdak(key string) {
	if _lme2 == nil {
		return
	}
	_lme2.Where("key = ?", key).Delete(&RuntimeConfig{})
}

type RuntimeData struct {
	APIKey     string
	Tier       string
	CustomerID int
}

func _he() (*RuntimeData, error) {
	_5i, err := _51(ConfigKeyAPIKey)
	if err != nil || _5i == "" {
		return nil, fmt.Errorf("no license found")
	}

	_silj, _ := _51(ConfigKeyTier)
	customerIDStr, _ := _51(ConfigKeyCustomerID)
	customerID, _ := strconv.Atoi(customerIDStr)

	return &RuntimeData{
		APIKey:     _5i,
		Tier:       _silj,
		CustomerID: customerID,
	}, nil
}

func _f1s(rd *RuntimeData) error {
	if err := _wa(ConfigKeyAPIKey, rd.APIKey); err != nil {
		return err
	}
	if err := _wa(ConfigKeyTier, rd.Tier); err != nil {
		return err
	}
	if rd.CustomerID > 0 {
		if err := _wa(ConfigKeyCustomerID, strconv.Itoa(rd.CustomerID)); err != nil {
			return err
		}
	}
	return nil
}

func _xqiz() {
	_jdak(ConfigKeyAPIKey)
	_jdak(ConfigKeyTier)
	_jdak(ConfigKeyCustomerID)
}

func _0y1() (string, error) {
	id, err := _51(ConfigKeyInstanceID)
	if err == nil && len(id) == 36 {
		return id, nil
	}

	id = _5iz()
	if id == "" {
		id, err = _i86i()
		if err != nil {
			return "", err
		}
	}

	if err := _wa(ConfigKeyInstanceID, id); err != nil {
		return "", err
	}
	return id, nil
}

func _5iz() string {
	hostname, _ := os.Hostname()
	macAddr := _e0v()
	if hostname == "" && macAddr == "" {
		return ""
	}

	seed := hostname + "|" + macAddr
	h := make([]byte, 16)
	copy(h, []byte(seed))
	for i := 16; i < len(seed); i++ {
		h[i%16] ^= seed[i]
	}
	h[6] = (h[6] & 0x0f) | 0x40 // _41t 4
	h[8] = (h[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func _e0v() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		if len(iface.HardwareAddr) > 0 {
			return iface.HardwareAddr.String()
		}
	}
	return ""
}

func _i86i() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

var _qf atomic.Value // set during activation

func init() {
	_qf.Store([]byte{0})
}

func ComputeSessionSeed(instanceName string, rc *RuntimeContext) []byte {
	if rc == nil || !rc._wq.Load() {
		return nil // Will cause panic in caller — intentional
	}
	h := sha256.New()
	h.Write([]byte(instanceName))
	h.Write([]byte(rc._5i))
	salt, _ := _qf.Load().([]byte)
	h.Write(salt)
	return h.Sum(nil)[:16]
}

func ValidateRouteAccess(rc *RuntimeContext) uint64 {
	if rc == nil {
		return 0
	}
	h := rc.ContextHash()
	return binary.LittleEndian.Uint64(h[:8])
}

func DeriveInstanceToken(_78z string, rc *RuntimeContext) string {
	if rc == nil || !rc._wq.Load() {
		return ""
	}
	h := sha256.Sum256([]byte(_78z + rc._5i))
	return _leb7(h[:8])
}

func _leb7(b []byte) string {
	const _i2n = "0123456789abcdef"
	dst := make([]byte, len(b)*2)
	for i, v := range b {
		dst[i*2] = _i2n[v>>4]
		dst[i*2+1] = _i2n[v&0x0f]
	}
	return string(dst)
}

func ActivateIntegrity(rc *RuntimeContext) {
	if rc == nil {
		return
	}
	h := sha256.Sum256([]byte(rc._5i + rc._78z + "ev0"))
	_qf.Store(h[:])
}

const (
	hbInterval = 30 * time.Minute
)

type RuntimeContext struct {
	_5i       string
	_k8 string // GLOBAL_API_KEY from .env — used as token for licensing check
	_78z   string
	_wq       atomic.Bool
	_77      [32]byte // Derived from activation — required by ValidateContext
	mu           sync.RWMutex
	_zkb6       string // Registration URL shown to users before activation
	_pr     string // Registration token for polling
	_silj         string
	_41t      string
	_xao      atomic.Int64 // Messages sent since last heartbeat
	_th5      atomic.Int64 // Messages received since last heartbeat
}

var _4sns atomic.Pointer[RuntimeContext]

func (rc *RuntimeContext) TrackMessage() {
	if rc != nil {
		rc._xao.Add(1)
	}
}

func TrackMessageSent() {
	if rc := _4sns.Load(); rc != nil {
		rc._xao.Add(1)
	}
}

func TrackMessageRecv() {
	if rc := _4sns.Load(); rc != nil {
		rc._th5.Add(1)
	}
}

func (rc *RuntimeContext) _b07h() int64 {
	return rc._xao.Swap(0)
}

func (rc *RuntimeContext) ContextHash() [32]byte {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc._77
}

func (rc *RuntimeContext) IsActive() bool {
	return rc._wq.Load()
}

func (rc *RuntimeContext) RegistrationURL() string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc._zkb6
}

func (rc *RuntimeContext) APIKey() string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc._5i
}

func (rc *RuntimeContext) InstanceID() string {
	return rc._78z
}

func InitializeRuntime(_silj, _41t, _k8 string) *RuntimeContext {
	if _silj == "" {
		_silj = "evolution-go"
	}
	if _41t == "" {
		_41t = "unknown"
	}

	rc := &RuntimeContext{
		_silj:         _silj,
		_41t:      _41t,
		_k8: _k8,
	}

	id, err := _0y1()
	if err != nil {
		log.Fatalf("[runtime] failed to initialize instance: %v", err)
	}
	rc._78z = id

	rd, err := _he()
	if err == nil && rd.APIKey != "" {
		rc._5i = rd.APIKey
		fmt.Printf("  ✓ License found: %s...%s\n", rd.APIKey[:8], rd.APIKey[len(rd.APIKey)-4:])

		rc._77 = sha256.Sum256([]byte(rc._5i + rc._78z))
		rc._wq.Store(true)
		ActivateIntegrity(rc)
		fmt.Println("  ✓ License activated successfully")

		go func() {
			if err := _yxbc(rc, _41t); err != nil {
				fmt.Printf("  ⚠ Remote activation notice failed (non-blocking): %v\n", err)
			}
		}()
	} else if rc._k8 != "" {
		rc._5i = rc._k8
		if err := _yxbc(rc, _41t); err == nil {
			_f1s(&RuntimeData{APIKey: rc._k8, Tier: _silj})
			rc._77 = sha256.Sum256([]byte(rc._5i + rc._78z))
			rc._wq.Store(true)
			ActivateIntegrity(rc)
			fmt.Printf("  ✓ GLOBAL_API_KEY accepted — license saved and activated\n")
		} else {
			rc._5i = ""
			_oac2()
			rc._wq.Store(false)
		}
	} else {
		_oac2()
		rc._wq.Store(false)
	}

	_4sns.Store(rc)

	return rc
}

func _oac2() {
	fmt.Println()
	fmt.Println("  ╔══════════════════════════════════════════════════════════╗")
	fmt.Println("  ║              License Registration Required               ║")
	fmt.Println("  ╚══════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("  Server starting without license.")
	fmt.Println("  API endpoints will return 503 until license is activated.")
	fmt.Println("  Use GET /license/register to get the registration URL.")
	fmt.Println()
}

func (rc *RuntimeContext) _3yll(authCodeOrKey, _silj string, customerID int) error {
	_5i, err := _sa72(authCodeOrKey)
	if err != nil {
		return fmt.Errorf("key exchange failed: %w", err)
	}

	rc.mu.Lock()
	rc._5i = _5i
	rc._zkb6 = ""
	rc._pr = ""
	rc.mu.Unlock()

	if err := _f1s(&RuntimeData{
		APIKey:     _5i,
		Tier:       _silj,
		CustomerID: customerID,
	}); err != nil {
		fmt.Printf("  ⚠ Warning: could not save license: %v\n", err)
	}

	if err := _yxbc(rc, rc._41t); err != nil {
		return err
	}

	rc.mu.Lock()
	rc._77 = sha256.Sum256([]byte(rc._5i + rc._78z))
	rc.mu.Unlock()
	rc._wq.Store(true)
	ActivateIntegrity(rc)

	fmt.Printf("  ✓ License activated! Key: %s...%s (_silj: %s)\n",
		_5i[:8], _5i[len(_5i)-4:], _silj)

	go func() {
		if err := _jc3d(rc, 0); err != nil {
			fmt.Printf("  ⚠ First heartbeat failed: %v\n", err)
		}
	}()

	return nil
}

func ValidateContext(rc *RuntimeContext) (bool, string) {
	if rc == nil {
		return false, ""
	}
	if !rc._wq.Load() {
		return false, rc.RegistrationURL()
	}
	expected := sha256.Sum256([]byte(rc._5i + rc._78z))
	actual := rc.ContextHash()
	if expected != actual {
		return false, ""
	}
	return true, ""
}

func GateMiddleware(rc *RuntimeContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		if path == "/health" || path == "/server/ok" || path == "/favicon.ico" ||
			path == "/license/status" || path == "/license/register" || path == "/license/activate" ||
			strings.HasPrefix(path, "/manager") || strings.HasPrefix(path, "/assets") ||
			strings.HasPrefix(path, "/swagger") || path == "/ws" ||
			strings.HasSuffix(path, ".svg") || strings.HasSuffix(path, ".css") ||
			strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".png") ||
			strings.HasSuffix(path, ".ico") || strings.HasSuffix(path, ".woff2") ||
			strings.HasSuffix(path, ".woff") || strings.HasSuffix(path, ".ttf") {
			c.Next()
			return
		}

		valid, _ := ValidateContext(rc)
		if !valid {
			scheme := "http"
			if c.Request.TLS != nil {
				scheme = "https"
			}
			managerURL := fmt.Sprintf("%s://%s/manager/login", scheme, c.Request.Host)

			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error":        "service not activated",
				"code":         "LICENSE_REQUIRED",
				"register_url": managerURL,
				"message":      "License required. Open the manager to activate your license.",
			})
			return
		}

		c.Set("_rch", rc.ContextHash())
		c.Next()
	}
}

func LicenseRoutes(eng *gin.Engine, rc *RuntimeContext) {
	lic := eng.Group("/license")
	{
		lic.GET("/status", func(c *gin.Context) {
			status := "inactive"
			if rc.IsActive() {
				status = "active"
			}

			resp := gin.H{
				"status":      status,
				"instance_id": rc._78z,
			}

			rc.mu.RLock()
			if rc._5i != "" {
				resp["api_key"] = rc._5i[:8] + "..." + rc._5i[len(rc._5i)-4:]
			}
			rc.mu.RUnlock()

			c.JSON(http.StatusOK, resp)
		})

		lic.GET("/register", func(c *gin.Context) {
			if rc.IsActive() {
				c.JSON(http.StatusOK, gin.H{
					"status":  "active",
					"message": "License is already active",
				})
				return
			}

			rc.mu.RLock()
			existingURL := rc._zkb6
			rc.mu.RUnlock()

			if existingURL != "" {
				c.JSON(http.StatusOK, gin.H{
					"status":       "pending",
					"register_url": existingURL,
				})
				return
			}

			payload := map[string]string{
				"tier":        rc._silj,
				"version":     rc._41t,
				"instance_id": rc._78z,
			}
			if redirectURI := c.Query("redirect_uri"); redirectURI != "" {
				payload["redirect_uri"] = redirectURI
			}

			resp, err := _5lk("/v1/register/init", payload)
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{
					"error":   "Failed to contact licensing server",
					"details": err.Error(),
				})
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				_zi0 := _r6(resp)
				c.JSON(resp.StatusCode, gin.H{
					"error":   "Licensing server error",
					"details": _zi0.Error(),
				})
				return
			}

			var _czi3 struct {
				RegisterURL string `json:"register_url"`
				Token       string `json:"token"`
			}
			json.NewDecoder(resp.Body).Decode(&_czi3)

			rc.mu.Lock()
			rc._zkb6 = _czi3.RegisterURL
			rc._pr = _czi3.Token
			rc.mu.Unlock()

			fmt.Printf("  → Registration URL: %s\n", _czi3.RegisterURL)

			c.JSON(http.StatusOK, gin.H{
				"status":       "pending",
				"register_url": _czi3.RegisterURL,
			})
		})

		lic.GET("/activate", func(c *gin.Context) {
			if rc.IsActive() {
				c.JSON(http.StatusOK, gin.H{
					"status":  "active",
					"message": "License is already active",
				})
				return
			}

			code := c.Query("code")
			if code == "" {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":   "Missing code parameter",
					"message": "Provide ?code=AUTHORIZATION_CODE from the registration callback.",
				})
				return
			}

			exchangeResp, err := _5lk("/v1/register/exchange", map[string]string{
				"authorization_code": code,
				"instance_id":       rc._78z,
			})
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{
					"error":   "Failed to contact licensing server",
					"details": err.Error(),
				})
				return
			}
			defer exchangeResp.Body.Close()

			if exchangeResp.StatusCode != http.StatusOK {
				_zi0 := _r6(exchangeResp)
				c.JSON(exchangeResp.StatusCode, gin.H{
					"error":   "Exchange failed",
					"details": _zi0.Error(),
				})
				return
			}

			var _9cn struct {
				APIKey     string `json:"api_key"`
				Tier       string `json:"tier"`
				CustomerID int    `json:"customer_id"`
			}
			json.NewDecoder(exchangeResp.Body).Decode(&_9cn)

			if _9cn.APIKey == "" {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":   "Invalid or expired code",
					"message": "The authorization code is invalid or has expired.",
				})
				return
			}

			if err := rc._3yll(_9cn.APIKey, _9cn.Tier, _9cn.CustomerID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "Activation failed",
					"details": err.Error(),
				})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"status":  "active",
				"message": "License activated successfully!",
			})
		})
	}
}

func StartHeartbeat(ctx context.Context, rc *RuntimeContext, startTime time.Time) {
	go func() {
		ticker := time.NewTicker(hbInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !rc.IsActive() {
					continue
				}
				uptime := int64(time.Since(startTime).Seconds())
				if err := _jc3d(rc, uptime); err != nil {
					fmt.Printf("  ⚠ Heartbeat failed (non-blocking): %v\n", err)
				}
			}
		}
	}()
}

func Shutdown(rc *RuntimeContext) {
	if rc == nil || rc._5i == "" {
		return
	}
	_qd(rc)
}

func _sioq(code string) (_5i string, err error) {
	resp, err := _5lk("/v1/register/exchange", map[string]string{
		"authorization_code": code,
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", _r6(resp)
	}

	var _9cn struct {
		APIKey string `json:"api_key"`
	}
	json.NewDecoder(resp.Body).Decode(&_9cn)
	if _9cn.APIKey == "" {
		return "", fmt.Errorf("exchange returned empty api_key")
	}
	return _9cn.APIKey, nil
}

func _sa72(authCodeOrKey string) (string, error) {
	_5i, err := _sioq(authCodeOrKey)
	if err == nil && _5i != "" {
		return _5i, nil
	}
	return authCodeOrKey, nil
}

func _yxbc(rc *RuntimeContext, _41t string) error {
	resp, err := _iv("/v1/activate", map[string]string{
		"instance_id": rc._78z,
		"version":     _41t,
	}, rc._5i)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return _r6(resp)
	}

	var _9cn struct {
		Status string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&_9cn)

	if _9cn.Status != "active" {
		return fmt.Errorf("activation returned status: %s", _9cn.Status)
	}
	return nil
}

func _jc3d(rc *RuntimeContext, uptimeSeconds int64) error {
	_xao := rc._b07h()
	_th5 := rc._th5.Swap(0)

	payload := map[string]any{
		"instance_id":    rc._78z,
		"uptime_seconds": uptimeSeconds,
		"version":        rc._41t,
	}

	if _xao > 0 || _th5 > 0 {
		bundle := map[string]any{}
		if _xao > 0 {
			bundle["messages_sent"] = _xao
		}
		if _th5 > 0 {
			bundle["messages_recv"] = _th5
		}
		payload["telemetry_bundle"] = bundle
	}

	resp, err := _iv("/v1/heartbeat", payload, rc._5i)
	if err != nil {
		rc._xao.Add(_xao)
		rc._th5.Add(_th5)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		rc._xao.Add(_xao)
		rc._th5.Add(_th5)
		return _r6(resp)
	}
	return nil
}

func _qd(rc *RuntimeContext) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	body, _ := json.Marshal(map[string]string{
		"instance_id": rc._78z,
	})

	url := _it2l() + "/v1/deactivate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", rc._5i)
	req.Header.Set("X-Signature", _trei(body, rc._5i))
	_j7g.Do(req)
}
