package callback

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/model"
)

const maxCallbackBody = 1 << 20

const (
	// callbackEventScope is deliberately not configurable.  The binding token
	// already authenticates an execution; a caller-controlled scope would let
	// the same token mint multiple replay identities.
	callbackEventScope         = "unified"
	callbackIngressConcurrency = 32
	callbackRateWindow         = time.Minute
	callbackRateLimit          = 120
)

var callbackIngressSemaphore = make(chan struct{}, callbackIngressConcurrency)

type callbackRateEntry struct {
	window time.Time
	count  uint32
}

var callbackRateState = struct {
	sync.Mutex
	entries map[string]callbackRateEntry
}{entries: make(map[string]callbackRateEntry)}

func callbackClientKey(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return "unknown"
	}
	// ClientIP may use a trusted proxy configuration.  That is preferable to
	// trusting an arbitrary header in deployments that have configured Gin's
	// trusted proxies; RemoteAddr remains the fallback for direct clients.
	if value := strings.TrimSpace(c.ClientIP()); value != "" {
		return value
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(c.Request.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(c.Request.RemoteAddr)
}

func allowCallbackRate(key string, now time.Time) bool {
	if key == "" {
		key = "unknown"
	}
	now = now.UTC()
	window := now.Truncate(callbackRateWindow)
	callbackRateState.Lock()
	defer callbackRateState.Unlock()
	// Opportunistically discard stale entries to keep an attacker from growing
	// this small process-local map without bound.
	for candidate, entry := range callbackRateState.entries {
		if window.Sub(entry.window) > callbackRateWindow {
			delete(callbackRateState.entries, candidate)
		}
	}
	entry := callbackRateState.entries[key]
	if entry.window != window {
		entry = callbackRateEntry{window: window, count: 0}
	}
	if entry.count >= callbackRateLimit {
		callbackRateState.entries[key] = entry
		return false
	}
	entry.count++
	callbackRateState.entries[key] = entry
	return true
}

func acquireCallbackIngress(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if !allowCallbackRate(callbackClientKey(c), time.Now()) {
		c.Header("Retry-After", "60")
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "callback rate limit exceeded"})
		return false
	}
	select {
	case callbackIngressSemaphore <- struct{}{}:
		return true
	default:
		c.Header("Retry-After", "1")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "callback ingress is busy"})
		return false
	}
}

func releaseCallbackIngress() {
	select {
	case <-callbackIngressSemaphore:
	default:
	}
}

func isJSONContentType(raw string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return mediaType == "application/json" || strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json")
}

// callbackKeySet contains only short-lived process memory. The handler clears
// every key before returning so a rotation does not leave old material alive
// in a request goroutine.
type callbackKeySet struct {
	keyringID  uint64
	kekVersion uint32
	keks       map[uint32][]byte
	hmacs      map[uint32][]byte
}

// HandleUnifiedCallback is deliberately a narrow ingress: authenticate the
// per-execution token, persist a Receipt/Alias, then return. It never changes
// task, billing or delivery state in the HTTP request.
func HandleUnifiedCallback(c *gin.Context) {
	if c == nil || c.Request == nil || c.Request.Method != http.MethodPost || !isJSONContentType(c.GetHeader("Content-Type")) {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "callback requires JSON POST"})
		return
	}
	if c.Request.ContentLength > maxCallbackBody {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "callback payload is too large"})
		return
	}
	if !acquireCallbackIngress(c) {
		return
	}
	defer releaseCallbackIngress()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	scope := callbackEventScope
	token := strings.TrimSpace(c.GetHeader("X-Gateway-Callback-Token"))
	decodedToken, decodeErr := base64.RawURLEncoding.DecodeString(token)
	validToken := decodeErr == nil && len(decodedToken) == 32 && base64.RawURLEncoding.EncodeToString(decodedToken) == token
	clear(decodedToken)
	if !validToken || strings.ContainsAny(token, "\r\n") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid callback authentication"})
		return
	}
	eventID := strings.TrimSpace(c.GetHeader("X-Gateway-Event-ID"))
	if len(eventID) > 255 || strings.ContainsAny(eventID, "\r\n") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid callback authentication"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxCallbackBody+1))
	if err != nil || len(body) == 0 || len(body) > maxCallbackBody {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "callback payload is too large"})
		return
	}
	defer clear(body)
	db, err := model.DB().DB()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "callback storage unavailable"})
		return
	}
	keys, err := loadCallbackKeys(ctx, db)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "callback authentication unavailable"})
		return
	}
	defer keys.clear()
	store, err := repository.New(db)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "callback storage unavailable"})
		return
	}

	// Every current/readable HMAC version is tried. A valid callback must map
	// all matching aliases to one execution and one encrypted token Blob.
	versions := orderedKeyVersions(keys.hmacs, keys.kekVersion)
	var binding repository.CallbackBindingRecord
	var matchedVersion uint32
	var tokenHMAC string
	for _, version := range versions {
		key := keys.hmacs[version]
		candidateHMAC := repository.CallbackBindingTokenHMAC(key, token)
		candidate, findErr := store.FindCallbackBinding(ctx, version, candidateHMAC)
		if findErr == repository.ErrNotFound {
			continue
		}
		if findErr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "callback storage unavailable"})
			return
		}
		if matchedVersion == 0 {
			binding, matchedVersion, tokenHMAC = candidate, version, candidateHMAC
			continue
		}
		if candidate.AsyncExecutionID != binding.AsyncExecutionID || candidate.CredentialVersionID != binding.CredentialVersionID || candidate.EncryptedBlobID != binding.EncryptedBlobID {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid callback authentication"})
			return
		}
	}
	if matchedVersion == 0 || binding.EncryptedBlobID == 0 || !verifyBindingBlob(ctx, store, binding, token, keys) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid callback authentication"})
		return
	}

	// An absent provider event ID still gets a stable identity. SHA-256 here is
	// only an input to the keyed event aliases; it is not used as authentication.
	if eventID == "" {
		digest := sha256.Sum256(body)
		eventID = hex.EncodeToString(digest[:])
	}
	eventAliases := make([]repository.CallbackReceiptAlias, 0, len(versions))
	payloadHMACs := make(map[uint32]string, len(versions))
	for _, version := range versions {
		key := keys.hmacs[version]
		aliasTokenHMAC := repository.CallbackBindingTokenHMAC(key, token)
		eventMAC := hmac.New(sha256.New, key)
		_, _ = eventMAC.Write([]byte(scope + "\x00" + aliasTokenHMAC + "\x00" + eventID))
		eventAliases = append(eventAliases, repository.CallbackReceiptAlias{HMACKeyVersion: version, EventHMAC: hex.EncodeToString(eventMAC.Sum(nil))})
		payloadMAC := security.HMACSHA256(key, body)
		payloadHMACs[version] = hex.EncodeToString(payloadMAC[:])
	}
	primaryEvent := ""
	for _, alias := range eventAliases {
		if alias.HMACKeyVersion == matchedVersion {
			primaryEvent = alias.EventHMAC
			break
		}
	}
	if primaryEvent == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid callback authentication"})
		return
	}
	primaryPayload := payloadHMACs[matchedVersion]
	currentHMAC := keys.hmacs[keys.kekVersion]
	if len(currentHMAC) != security.KeySize {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "callback authentication unavailable"})
		return
	}
	service, err := gatewayruntime.New(store)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "callback runtime unavailable"})
		return
	}
	receiptID, replay, err := service.IngestCallback(ctx, gatewayruntime.CallbackReceiptInput{
		AsyncExecutionID: binding.AsyncExecutionID, CredentialVersionID: binding.CredentialVersionID,
		EventScope: scope, EventID: eventID, EventHMAC: primaryEvent, EventAliases: eventAliases,
		PayloadHMAC: primaryPayload, PayloadHMACs: payloadHMACs,
		BindingTokenHMAC: tokenHMAC, BindingTokenHMACKeyVer: matchedVersion,
		Payload: body, PayloadKeyringID: keys.keyringID, PayloadKEKVersion: keys.kekVersion,
		PayloadKEK: keys.keks[keys.kekVersion], PayloadHMACKey: currentHMAC,
		ExpiresAt: callbackExpiry(),
	})
	if err != nil {
		status := http.StatusInternalServerError
		if err == repository.ErrConflict || err == repository.ErrInvalidInput {
			status = http.StatusUnauthorized
		}
		c.JSON(status, gin.H{"error": "callback receipt failed"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"receipt_id": receiptID, "replay": replay})
}

func callbackExpiry() time.Time {
	return time.Now().UTC().Add(24 * time.Hour)
}

func orderedKeyVersions(keys map[uint32][]byte, current uint32) []uint32 {
	versions := make([]uint32, 0, len(keys))
	for version := range keys {
		if version == current {
			continue
		}
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	if _, exists := keys[current]; exists {
		versions = append([]uint32{current}, versions...)
	}
	return versions
}

func (k *callbackKeySet) clear() {
	if k == nil {
		return
	}
	for version, key := range k.keks {
		clear(key)
		delete(k.keks, version)
	}
	security.ClearHMACKeys(k.hmacs)
}

func loadCallbackKeys(ctx context.Context, db *sql.DB) (callbackKeySet, error) {
	if db == nil {
		return callbackKeySet{}, security.ErrInvalidKey
	}
	var out callbackKeySet
	out.keks = make(map[uint32][]byte)
	out.hmacs = make(map[uint32][]byte)
	rows, err := db.QueryContext(ctx, `SELECT k.id,k.current_version,v.key_version,v.status,v.provider_key_ref
FROM crypto_keyring_state k
JOIN crypto_key_versions v ON v.keyring_id=k.id
WHERE k.purpose='gateway-payload' AND v.status IN ('current','readable')
ORDER BY v.key_version`)
	if err != nil {
		out.clear()
		return callbackKeySet{}, err
	}
	defer rows.Close()
	var currentVersion uint32
	for rows.Next() {
		var id, ringCurrent uint64
		var version uint32
		var status, providerRef string
		if err := rows.Scan(&id, &ringCurrent, &version, &status, &providerRef); err != nil {
			out.clear()
			return callbackKeySet{}, err
		}
		if out.keyringID == 0 {
			out.keyringID = id
			currentVersion = uint32(ringCurrent)
		} else if out.keyringID != id {
			out.clear()
			return callbackKeySet{}, security.ErrInvalidKey
		}
		if version == 0 || providerRef == "" || !strings.HasPrefix(providerRef, "env:") {
			out.clear()
			return callbackKeySet{}, security.ErrInvalidKey
		}
		kek, keyErr := loadEnvKey(strings.TrimPrefix(providerRef, "env:"))
		if keyErr != nil {
			out.clear()
			return callbackKeySet{}, keyErr
		}
		out.keks[version] = kek
		// HMAC keys use the same logical version set but a separate base env.
		hmacKey, hmacErr := security.LoadVersionedHMACKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64", version)
		if hmacErr != nil {
			out.clear()
			return callbackKeySet{}, hmacErr
		}
		out.hmacs[version] = hmacKey
		if status == "current" {
			out.kekVersion = version
		}
	}
	if err := rows.Err(); err != nil {
		out.clear()
		return callbackKeySet{}, err
	}
	if out.keyringID == 0 || out.kekVersion == 0 || currentVersion != out.kekVersion || len(out.keks[out.kekVersion]) != security.KeySize {
		out.clear()
		return callbackKeySet{}, security.ErrInvalidKey
	}
	return out, nil
}

func loadEnvKey(name string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(strings.TrimSpace(name)))
	if raw == "" {
		return nil, security.ErrInvalidKey
	}
	return security.DecodeBase64Key(raw)
}

func verifyBindingBlob(ctx context.Context, store *repository.Store, binding repository.CallbackBindingRecord, token string, keys callbackKeySet) bool {
	envelope, err := store.ReadEncryptedBlob(ctx, store.DB(), binding.EncryptedBlobID)
	if err != nil || envelope.Purpose != "gateway-callback-binding-token" || envelope.SchemaVersion != 1 {
		return false
	}
	owner := []byte(fmt.Sprintf("async:%d:callback-binding-token", binding.AsyncExecutionID))
	kek := keys.keks[envelope.KEKVersion]
	if len(kek) != security.KeySize {
		return false
	}
	for _, hmacKey := range keys.hmacs {
		plain, openErr := repository.OpenBlob(envelope, binding.EncryptedBlobID, owner, kek, hmacKey)
		if openErr != nil {
			continue
		}
		valid := len(plain) == len(token) && subtle.ConstantTimeCompare(plain, []byte(token)) == 1
		clear(plain)
		if valid {
			return true
		}
	}
	return false
}
