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

var _k1 = []byte{0xe1, 0x54, 0x55, 0x3a, 0x70, 0x1b, 0x2f, 0xc2, 0x99, 0x9f, 0xe8, 0x1d, 0xd3, 0xfe, 0xe2, 0xc8, 0x37, 0x9e, 0xfd, 0x1d, 0xb1, 0x0b, 0x1c, 0x0f, 0xbc, 0x35, 0xbd, 0x7b, 0xb1, 0xe0, 0xfc, 0x92, 0x2a, 0x60, 0xab, 0x27, 0xc1, 0xc4, 0x2b, 0xe2, 0x34, 0x46}
var _k0 = []byte{0x89, 0x20, 0x21, 0x4a, 0x03, 0x21, 0x00, 0xed, 0xf5, 0xf6, 0x8b, 0x78, 0xbd, 0x8d, 0x87, 0xe6, 0x52, 0xe8, 0x92, 0x71, 0xc4, 0x7f, 0x75, 0x60, 0xd2, 0x53, 0xd2, 0x0e, 0xdf, 0x84, 0x9d, 0xe6, 0x43, 0x0f, 0xc5, 0x09, 0xa2, 0xab, 0x46, 0xcc, 0x56, 0x34}

var (
	_3m string
	_d921    string
)

func _2x() string {
	if _3m != "" && _d921 != "" {
		return _g0v(_3m, _d921)
	}
	parts := [...]string{"h", "tt", "ps", "://", "li", "ce", "nse", ".", "ev", "ol", "ut", "io", "nf", "ou", "nd", "at", "io", "n.", "co", "m.", "br"}
	var s string
	for _, p := range parts {
		s += p
	}
	return s
}

func _g0v(enc, key string) string {
	encBytes := _sb9k(enc)
	keyBytes := _sb9k(key)
	if len(keyBytes) == 0 {
		return ""
	}
	out := make([]byte, len(encBytes))
	for i, b := range encBytes {
		out[i] = b ^ keyBytes[i%len(keyBytes)]
	}
	return string(out)
}

func _sb9k(s string) []byte {
	if len(s)%2 != 0 {
		return nil
	}
	b := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		b[i/2] = _ya(s[i])<<4 | _ya(s[i+1])
	}
	return b
}

func _ya(c byte) byte {
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

var _koh = &http.Client{Timeout: 10 * time.Second}

func _l4(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func _9k(path string, payload interface{}, _nq string) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := _2x() + path
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", _nq)
	req.Header.Set("X-Signature", _l4(body, _nq))

	return _koh.Do(req)
}

func _8z(path string) (*http.Response, error) {
	url := _2x() + path
	return _koh.Get(url)
}

func _rf(path string, payload interface{}) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := _2x() + path
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return _koh.Do(req)
}

func _lsm(resp *http.Response) error {
	b, _ := io.ReadAll(resp.Body)
	var _jm struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(b, &_jm); err == nil {
		msg := _jm.Message
		if msg == "" {
			msg = _jm.Error
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

var _h86d *gorm.DB

func SetDB(db *gorm.DB) {
	_h86d = db
}

func MigrateDB() error {
	if _h86d == nil {
		return fmt.Errorf("core: database not set, call SetDB first")
	}
	return _h86d.AutoMigrate(&RuntimeConfig{})
}

func _zb4a(key string) (string, error) {
	if _h86d == nil {
		return "", fmt.Errorf("core: database not set")
	}
	var _fvu RuntimeConfig
	_4b := _h86d.Where("key = ?", key).First(&_fvu)
	if _4b.Error != nil {
		return "", _4b.Error
	}
	return _fvu.Value, nil
}

func _j8r(key, value string) error {
	if _h86d == nil {
		return fmt.Errorf("core: database not set")
	}
	var _fvu RuntimeConfig
	_4b := _h86d.Where("key = ?", key).First(&_fvu)
	if _4b.Error != nil {
		return _h86d.Create(&RuntimeConfig{Key: key, Value: value}).Error
	}
	return _h86d.Model(&_fvu).Update("value", value).Error
}

func _iyq(key string) {
	if _h86d == nil {
		return
	}
	_h86d.Where("key = ?", key).Delete(&RuntimeConfig{})
}

type RuntimeData struct {
	APIKey     string
	Tier       string
	CustomerID int
}

func _d6cr() (*RuntimeData, error) {
	_nq, err := _zb4a(ConfigKeyAPIKey)
	if err != nil || _nq == "" {
		return nil, fmt.Errorf("no license found")
	}

	_rrdn, _ := _zb4a(ConfigKeyTier)
	customerIDStr, _ := _zb4a(ConfigKeyCustomerID)
	customerID, _ := strconv.Atoi(customerIDStr)

	return &RuntimeData{
		APIKey:     _nq,
		Tier:       _rrdn,
		CustomerID: customerID,
	}, nil
}

func _z5(rd *RuntimeData) error {
	if err := _j8r(ConfigKeyAPIKey, rd.APIKey); err != nil {
		return err
	}
	if err := _j8r(ConfigKeyTier, rd.Tier); err != nil {
		return err
	}
	if rd.CustomerID > 0 {
		if err := _j8r(ConfigKeyCustomerID, strconv.Itoa(rd.CustomerID)); err != nil {
			return err
		}
	}
	return nil
}

func _cfyr() {
	_iyq(ConfigKeyAPIKey)
	_iyq(ConfigKeyTier)
	_iyq(ConfigKeyCustomerID)
}

func _s5() (string, error) {
	id, err := _zb4a(ConfigKeyInstanceID)
	if err == nil && len(id) == 36 {
		return id, nil
	}

	id = _azq()
	if id == "" {
		id, err = _5hg()
		if err != nil {
			return "", err
		}
	}

	if err := _j8r(ConfigKeyInstanceID, id); err != nil {
		return "", err
	}
	return id, nil
}

func _azq() string {
	hostname, _ := os.Hostname()
	macAddr := _ag3a()
	if hostname == "" && macAddr == "" {
		return ""
	}

	seed := hostname + "|" + macAddr
	h := make([]byte, 16)
	copy(h, []byte(seed))
	for i := 16; i < len(seed); i++ {
		h[i%16] ^= seed[i]
	}
	h[6] = (h[6] & 0x0f) | 0x40 // _pg 4
	h[8] = (h[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func _ag3a() string {
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

func _5hg() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

var _q8 atomic.Value // set during activation

func init() {
	_q8.Store([]byte{0})
}

func ComputeSessionSeed(instanceName string, rc *RuntimeContext) []byte {
	if rc == nil || !rc._yl.Load() {
		return nil // Will cause panic in caller — intentional
	}
	h := sha256.New()
	h.Write([]byte(instanceName))
	h.Write([]byte(rc._nq))
	salt, _ := _q8.Load().([]byte)
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

func DeriveInstanceToken(_0o7p string, rc *RuntimeContext) string {
	if rc == nil || !rc._yl.Load() {
		return ""
	}
	h := sha256.Sum256([]byte(_0o7p + rc._nq))
	return _u96k(h[:8])
}

func _u96k(b []byte) string {
	const _89jh = "0123456789abcdef"
	dst := make([]byte, len(b)*2)
	for i, v := range b {
		dst[i*2] = _89jh[v>>4]
		dst[i*2+1] = _89jh[v&0x0f]
	}
	return string(dst)
}

func ActivateIntegrity(rc *RuntimeContext) {
	if rc == nil {
		return
	}
	h := sha256.Sum256([]byte(rc._nq + rc._0o7p + "ev0"))
	_q8.Store(h[:])
}

const (
	hbInterval = 30 * time.Minute
)

type RuntimeContext struct {
	_nq       string
	_o235 string // GLOBAL_API_KEY from .env — used as token for licensing check
	_0o7p   string
	_yl       atomic.Bool
	_hsd      [32]byte // Derived from activation — required by ValidateContext
	mu           sync.RWMutex
	_n8r       string // Registration URL shown to users before activation
	_xfjc     string // Registration token for polling
	_rrdn         string
	_pg      string
}

func (rc *RuntimeContext) ContextHash() [32]byte {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc._hsd
}

func (rc *RuntimeContext) IsActive() bool {
	return rc._yl.Load()
}

func (rc *RuntimeContext) RegistrationURL() string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc._n8r
}

func (rc *RuntimeContext) APIKey() string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc._nq
}

func (rc *RuntimeContext) InstanceID() string {
	return rc._0o7p
}

func InitializeRuntime(_rrdn, _pg, _o235 string) *RuntimeContext {
	if _rrdn == "" {
		_rrdn = "evolution-go"
	}
	if _pg == "" {
		_pg = "unknown"
	}

	rc := &RuntimeContext{
		_rrdn:         _rrdn,
		_pg:      _pg,
		_o235: _o235,
	}

	id, err := _s5()
	if err != nil {
		log.Fatalf("[runtime] failed to initialize instance: %v", err)
	}
	rc._0o7p = id

	rd, err := _d6cr()
	if err == nil && rd.APIKey != "" {
		rc._nq = rd.APIKey
		fmt.Printf("  ✓ License found: %s...%s\n", rd.APIKey[:8], rd.APIKey[len(rd.APIKey)-4:])

		rc._hsd = sha256.Sum256([]byte(rc._nq + rc._0o7p))
		rc._yl.Store(true)
		ActivateIntegrity(rc)
		fmt.Println("  ✓ License activated successfully")

		go func() {
			if err := _yi(rc, _pg); err != nil {
				fmt.Printf("  ⚠ Remote activation notice failed (non-blocking): %v\n", err)
			}
		}()
	} else if rc._o235 != "" {
		rc._nq = rc._o235
		if err := _yi(rc, _pg); err == nil {
			_z5(&RuntimeData{APIKey: rc._o235, Tier: _rrdn})
			rc._hsd = sha256.Sum256([]byte(rc._nq + rc._0o7p))
			rc._yl.Store(true)
			ActivateIntegrity(rc)
			fmt.Printf("  ✓ GLOBAL_API_KEY accepted — license saved and activated\n")
		} else {
			rc._nq = ""
			_pmzf()
			rc._yl.Store(false)
		}
	} else {
		_pmzf()
		rc._yl.Store(false)
	}

	return rc
}

func _pmzf() {
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

func (rc *RuntimeContext) _kex(authCodeOrKey, _rrdn string, customerID int) error {
	_nq, err := _qjr5(authCodeOrKey)
	if err != nil {
		return fmt.Errorf("key exchange failed: %w", err)
	}

	rc.mu.Lock()
	rc._nq = _nq
	rc._n8r = ""
	rc._xfjc = ""
	rc.mu.Unlock()

	if err := _z5(&RuntimeData{
		APIKey:     _nq,
		Tier:       _rrdn,
		CustomerID: customerID,
	}); err != nil {
		fmt.Printf("  ⚠ Warning: could not save license: %v\n", err)
	}

	if err := _yi(rc, rc._pg); err != nil {
		return err
	}

	rc.mu.Lock()
	rc._hsd = sha256.Sum256([]byte(rc._nq + rc._0o7p))
	rc.mu.Unlock()
	rc._yl.Store(true)
	ActivateIntegrity(rc)

	fmt.Printf("  ✓ License activated! Key: %s...%s (_rrdn: %s)\n",
		_nq[:8], _nq[len(_nq)-4:], _rrdn)

	go func() {
		if err := _qfvq(rc, 0); err != nil {
			fmt.Printf("  ⚠ First heartbeat failed: %v\n", err)
		}
	}()

	return nil
}

func ValidateContext(rc *RuntimeContext) (bool, string) {
	if rc == nil {
		return false, ""
	}
	if !rc._yl.Load() {
		return false, rc.RegistrationURL()
	}
	expected := sha256.Sum256([]byte(rc._nq + rc._0o7p))
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
				"instance_id": rc._0o7p,
			}

			rc.mu.RLock()
			if rc._nq != "" {
				resp["api_key"] = rc._nq[:8] + "..." + rc._nq[len(rc._nq)-4:]
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
			existingURL := rc._n8r
			rc.mu.RUnlock()

			if existingURL != "" {
				c.JSON(http.StatusOK, gin.H{
					"status":       "pending",
					"register_url": existingURL,
				})
				return
			}

			payload := map[string]string{
				"tier":        rc._rrdn,
				"version":     rc._pg,
				"instance_id": rc._0o7p,
			}
			if redirectURI := c.Query("redirect_uri"); redirectURI != "" {
				payload["redirect_uri"] = redirectURI
			}

			resp, err := _rf("/v1/register/init", payload)
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{
					"error":   "Failed to contact licensing server",
					"details": err.Error(),
				})
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				_jm := _lsm(resp)
				c.JSON(resp.StatusCode, gin.H{
					"error":   "Licensing server error",
					"details": _jm.Error(),
				})
				return
			}

			var _0z struct {
				RegisterURL string `json:"register_url"`
				Token       string `json:"token"`
			}
			json.NewDecoder(resp.Body).Decode(&_0z)

			rc.mu.Lock()
			rc._n8r = _0z.RegisterURL
			rc._xfjc = _0z.Token
			rc.mu.Unlock()

			fmt.Printf("  → Registration URL: %s\n", _0z.RegisterURL)

			c.JSON(http.StatusOK, gin.H{
				"status":       "pending",
				"register_url": _0z.RegisterURL,
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

			exchangeResp, err := _rf("/v1/register/exchange", map[string]string{
				"authorization_code": code,
				"instance_id":       rc._0o7p,
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
				_jm := _lsm(exchangeResp)
				c.JSON(exchangeResp.StatusCode, gin.H{
					"error":   "Exchange failed",
					"details": _jm.Error(),
				})
				return
			}

			var _4b struct {
				APIKey     string `json:"api_key"`
				Tier       string `json:"tier"`
				CustomerID int    `json:"customer_id"`
			}
			json.NewDecoder(exchangeResp.Body).Decode(&_4b)

			if _4b.APIKey == "" {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":   "Invalid or expired code",
					"message": "The authorization code is invalid or has expired.",
				})
				return
			}

			if err := rc._kex(_4b.APIKey, _4b.Tier, _4b.CustomerID); err != nil {
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
				if err := _qfvq(rc, uptime); err != nil {
					fmt.Printf("  ⚠ Heartbeat failed (non-blocking): %v\n", err)
				}
			}
		}
	}()
}

func Shutdown(rc *RuntimeContext) {
	if rc == nil || rc._nq == "" {
		return
	}
	_uo1b(rc)
}

func _vzi(code string) (_nq string, err error) {
	resp, err := _rf("/v1/register/exchange", map[string]string{
		"authorization_code": code,
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", _lsm(resp)
	}

	var _4b struct {
		APIKey string `json:"api_key"`
	}
	json.NewDecoder(resp.Body).Decode(&_4b)
	if _4b.APIKey == "" {
		return "", fmt.Errorf("exchange returned empty api_key")
	}
	return _4b.APIKey, nil
}

func _qjr5(authCodeOrKey string) (string, error) {
	_nq, err := _vzi(authCodeOrKey)
	if err == nil && _nq != "" {
		return _nq, nil
	}
	return authCodeOrKey, nil
}

func _yi(rc *RuntimeContext, _pg string) error {
	resp, err := _9k("/v1/activate", map[string]string{
		"instance_id": rc._0o7p,
		"version":     _pg,
	}, rc._nq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return _lsm(resp)
	}

	var _4b struct {
		Status string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&_4b)

	if _4b.Status != "active" {
		return fmt.Errorf("activation returned status: %s", _4b.Status)
	}
	return nil
}

func _qfvq(rc *RuntimeContext, uptimeSeconds int64) error {
	resp, err := _9k("/v1/heartbeat", map[string]any{
		"instance_id":    rc._0o7p,
		"uptime_seconds": uptimeSeconds,
		"version":        rc._pg,
	}, rc._nq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return _lsm(resp)
	}
	return nil
}

func _uo1b(rc *RuntimeContext) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	body, _ := json.Marshal(map[string]string{
		"instance_id": rc._0o7p,
	})

	url := _2x() + "/v1/deactivate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", rc._nq)
	req.Header.Set("X-Signature", _l4(body, rc._nq))
	_koh.Do(req)
}
