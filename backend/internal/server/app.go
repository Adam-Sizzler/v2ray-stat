package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/exodus/subscription-page/backend/internal/assets"
	"github.com/exodus/subscription-page/backend/internal/config"
	"github.com/exodus/subscription-page/backend/internal/logger"
	"github.com/exodus/subscription-page/backend/internal/proto"
	"github.com/exodus/subscription-page/backend/internal/security"
	"github.com/exodus/subscription-page/backend/internal/srslists"
)

const sessionCookieName = "session"

const (
	bridgeOperationSubscriptionInfo    = "subscription_info"
	bridgeOperationSubscriptionContent = "subscription_content"
	bridgeOperationSubpageByShortUUID  = "subpage_config_for_short"
	bridgeOperationSubpageByUUID       = "subpage_config_by_uuid"
)

var (
	appConfigPaths = map[string]struct{}{
		"/assets/app-config.json": {},
	}

	allowedClientTypes = map[string]struct{}{
		"stash":      {},
		"singbox":    {},
		"mihomo":     {},
		"json":       {},
		"v2ray-json": {},
		"clash":      {},
	}

	browserKeywords = []string{
		"Mozilla",
		"Chrome",
		"Safari",
		"Firefox",
		"Opera",
		"Edge",
		"TelegramBot",
		"WhatsApp",
	}

	genericPathFragments = []string{
		"favicon.ico",
		"robots.txt",
		".png",
		".jpg",
		".jpeg",
		".gif",
		".svg",
		".webp",
		".ico",
	}
)

type PanelBridge interface {
	QueryPanel(context.Context, *proto.SubscriptionBridgeRequest) (*proto.SubscriptionBridgeResponse, error)
	GetCachedSubpageConfig(uuid string) ([]byte, bool)
}

type App struct {
	cfg            config.Config
	bridge         PanelBridge
	assetsPath     string
	indexHTML      string
	assetsReplacer *strings.Replacer
	jsCacheMu      sync.RWMutex
	jsCache        map[string][]byte
}

type subpageConfigByShortEnvelope struct {
	Response struct {
		SubpageConfigUUID string `json:"subpageConfigUuid"`
		WebpageAllowed    bool   `json:"webpageAllowed"`
	} `json:"response"`
}

type baseSettingsEnvelope struct {
	BaseSettings struct {
		MetaTitle          string `json:"metaTitle"`
		MetaDescription    string `json:"metaDescription"`
		ShowConnectionKeys bool   `json:"showConnectionKeys"`
	} `json:"baseSettings"`
}

type baseSettings struct {
	MetaTitle          string
	MetaDescription    string
	ShowConnectionKeys bool
}

type accessLogResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (w *accessLogResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *accessLogResponseWriter) Write(payload []byte) (int, error) {
	written, err := w.ResponseWriter.Write(payload)
	w.bytesWritten += written
	return written, err
}

func (w *accessLogResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func shouldLogHTTPRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}

	path := cleanPath(r.URL.Path)
	segments := splitSegments(path)
	for _, segment := range segments {
		switch segment {
		case "assets", "locales":
			return false
		}
	}

	return true
}

func formatHTTPAccessLog(r *http.Request, statusCode, bytesWritten int, elapsed time.Duration) string {
	remoteAddr := getRealIP(r, "1")
	if remoteAddr == "" {
		remoteAddr = "-"
	}
	referrer := r.Referer()
	if referrer == "" {
		referrer = "-"
	}
	userAgent := r.UserAgent()
	if userAgent == "" {
		userAgent = "-"
	}
	contentLength := "-"
	if bytesWritten > 0 {
		contentLength = fmt.Sprintf("%d", bytesWritten)
	}
	return fmt.Sprintf(`%s - "%s %s HTTP/%d.%d" %d %s "%s" "%s" %s`,
		remoteAddr,
		r.Method,
		r.URL.RequestURI(),
		r.ProtoMajor,
		r.ProtoMinor,
		statusCode,
		contentLength,
		referrer,
		userAgent,
		elapsed.Round(time.Millisecond),
	)
}

func New(cfg config.Config, bridge PanelBridge) (*App, error) {
	if bridge == nil {
		return nil, fmt.Errorf("panel bridge is required")
	}

	assetsPath, err := assets.DetectPath()
	if err != nil {
		return nil, err
	}

	logger.WithContext("Bootstrap").Debugf("[CONFIG] assets path: %s", assetsPath)

	var indexHTML string
	if indexBytes, readErr := os.ReadFile(filepath.Join(assetsPath, "index.html")); readErr == nil {
		indexHTML = string(indexBytes)
	}

	var assetsReplacer *strings.Replacer
	if trimmed := cfg.Backend.Trimmed(); trimmed != "" {
		assetPrefix := "/" + strings.Trim(trimmed, "/") + "/"
		assetsReplacer = strings.NewReplacer(
			`"/assets/`, `"`+assetPrefix+`assets/`,
			`'/assets/`, `'`+assetPrefix+`assets/`,
			`(/assets/`, `(`+assetPrefix+`assets/`,
			`"/locales/`, `"`+assetPrefix+`locales/`,
			`'/locales/`, `'`+assetPrefix+`locales/`,
			`(/locales/`, `(`+assetPrefix+`locales/`,
		)
	}

	return &App{
		cfg:            cfg,
		bridge:         bridge,
		assetsPath:     assetsPath,
		indexHTML:      indexHTML,
		assetsReplacer: assetsReplacer,
		jsCache:        make(map[string][]byte),
	}, nil
}

func (a *App) getIndexHTML() (string, error) {
	if a.indexHTML != "" {
		return a.indexHTML, nil
	}
	indexBytes, err := os.ReadFile(filepath.Join(a.assetsPath, "index.html"))
	if err != nil {
		return "", err
	}
	a.indexHTML = string(indexBytes)
	return a.indexHTML, nil
}

func (a *App) prefixAssetsInHTML(content string) string {
	if a.assetsReplacer == nil {
		return content
	}
	return a.assetsReplacer.Replace(content)
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if shouldLogHTTPRequest(r) {
		recorder := &accessLogResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		start := time.Now()
		defer func() {
			logger.WithContext("HTTP").Debug(formatHTTPAccessLog(r, recorder.statusCode, recorder.bytesWritten, time.Since(start)))
		}()
		w = recorder
	}

	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet, noimageindex")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		closeConnection(w)
		return
	}

	if !a.cfg.IsDevelopment() {
		if strings.TrimSpace(r.Header.Get("X-Forwarded-For")) == "" ||
			r.Header.Get("X-Forwarded-Proto") != "https" {
			logger.WithContext("ProxyCheckMiddleware").Debugf(
				"X-Forwarded-For: %s, X-Forwarded-Proto: %s",
				r.Header.Get("X-Forwarded-For"),
				r.Header.Get("X-Forwarded-Proto"),
			)
			logger.WithContext("ProxyCheckMiddleware").Errorf("Reverse proxy and HTTPS are required.")
			closeConnection(w)
			return
		}
	}

	requestPath := cleanPath(r.URL.Path)
	routePath, ok := a.applyCustomPrefix(requestPath)
	if !ok {
		closeConnection(w)
		return
	}

	if srslists.IsRulesetRoute(routePath) {
		srslists.ServeHTTP(w, r, routePath)
		return
	}

	if a.isAppConfigPath(routePath) {
		a.handleAppConfig(w, r)
		return
	}

	if strings.HasPrefix(routePath, "/assets") || strings.HasPrefix(routePath, "/locales") {
		if _, err := a.verifySessionCookie(r); err != nil {
			logger.WithContext("CheckAssetsCookieMiddleware").Debugf("%v", err)
			closeConnection(w)
			return
		}

		a.serveStatic(w, r, routePath)
		return
	}

	segments := splitSegments(routePath)
	if len(segments) != 1 && len(segments) != 2 {
		closeConnection(w)
		return
	}

	shortUUID := segments[0]
	clientType := ""
	if len(segments) == 2 {
		clientType = segments[1]
		if _, ok := allowedClientTypes[clientType]; !ok {
			logger.WithContext("RootController").Errorf("Invalid client type: %s", clientType)
			closeConnection(w)
			return
		}
	}

	if a.isGenericPath(routePath) {
		closeConnection(w)
		return
	}

	clientIP := getRealIP(r, a.cfg.TrustProxy)
	resolvedShortUUID, err := a.resolveShortUUID(r.Context(), clientIP, shortUUID)
	if err != nil {
		logger.WithContext("RootService").Debugf("short uuid resolution failed for %s: %v", shortUUID, err)
		closeConnection(w)
		return
	}

	if a.isBrowser(r.UserAgent()) {
		a.returnWebpage(clientIP, resolvedShortUUID, w, r)
		return
	}

	a.proxySubscription(clientIP, resolvedShortUUID, clientType, w, r)
}

func (a *App) handleAppConfig(w http.ResponseWriter, r *http.Request) {
	claims, err := a.verifySessionCookie(r)
	if err != nil {
		logger.WithContext("CheckAssetsCookieMiddleware").Debugf("%v", err)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	subpageConfigUUID := strings.TrimSpace(claims.SubpageConfigUUID)
	if subpageConfigUUID == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "subpage_config_not_found"})
		return
	}

	subpageConfigRaw, err := a.getSubpageConfigByUUID(r.Context(), subpageConfigUUID)
	if err != nil {
		logger.WithContext("RootService").Errorf("%v", err)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed_to_fetch_subpage_config"})
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(subpageConfigRaw)
}

func (a *App) serveStatic(w http.ResponseWriter, r *http.Request, requestPath string) {
	if containsDotfile(requestPath) {
		closeConnection(w)
		return
	}

	trimmedPath := strings.TrimPrefix(requestPath, "/")
	fullPath := filepath.Join(a.assetsPath, filepath.FromSlash(trimmedPath))
	cleanAssetsRoot := filepath.Clean(a.assetsPath)
	cleanFullPath := filepath.Clean(fullPath)

	if cleanFullPath != cleanAssetsRoot &&
		!strings.HasPrefix(cleanFullPath, cleanAssetsRoot+string(filepath.Separator)) {
		closeConnection(w)
		return
	}

	info, err := os.Stat(cleanFullPath)
	if err != nil || info.IsDir() {
		closeConnection(w)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")

	if a.cfg.SubPath != "" && strings.HasSuffix(cleanFullPath, ".js") {
		a.jsCacheMu.RLock()
		cached, ok := a.jsCache[cleanFullPath]
		a.jsCacheMu.RUnlock()
		if ok {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			if r.Method != http.MethodHead {
				_, _ = w.Write(cached)
			}
			return
		}

		content, readErr := os.ReadFile(cleanFullPath)
		if readErr == nil {
			prefix := a.cfg.SubPath
			rewritten := strings.ReplaceAll(
				string(content),
				`"/assets/app-config.json"`,
				fmt.Sprintf("%q", prefix+"/assets/app-config.json"),
			)
			rewrittenBytes := []byte(rewritten)

			a.jsCacheMu.Lock()
			if a.jsCache == nil {
				a.jsCache = make(map[string][]byte)
			}
			a.jsCache[cleanFullPath] = rewrittenBytes
			a.jsCacheMu.Unlock()

			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			if r.Method != http.MethodHead {
				_, _ = w.Write(rewrittenBytes)
			}
			return
		}
	}

	http.ServeFile(&staticResponseWriter{w}, r, cleanFullPath)
}

// staticResponseWriter strips ETag/Last-Modified that http.ServeFile/
// ServeContent would otherwise set, matching the upstream sirv static
// server (maxAge: 604800, immutable: true, etag: false).
type staticResponseWriter struct {
	http.ResponseWriter
}

func (w *staticResponseWriter) WriteHeader(statusCode int) {
	w.Header().Del("Etag")
	w.Header().Del("Last-Modified")
	w.ResponseWriter.WriteHeader(statusCode)
}

func (a *App) proxySubscription(
	clientIP, shortUUID, clientType string,
	w http.ResponseWriter,
	r *http.Request,
) {
	bridgeResp, err := a.bridge.QueryPanel(r.Context(), &proto.SubscriptionBridgeRequest{
		Operation:  bridgeOperationSubscriptionContent,
		ShortUuid:  shortUUID,
		ClientType: clientType,
		ClientIp:   clientIP,
		Headers:    filterAndConvertToProtoHeaders(r.Header),
	})
	if err != nil {
		logger.WithContext("RootService").Errorf("Error in GetSubscription Request: %v", err)
		closeConnection(w)
		return
	}

	if bridgeResp.GetStatusCode() < 200 || bridgeResp.GetStatusCode() >= 300 || len(bridgeResp.GetPayload()) == 0 {
		if bridgeResp.GetStatusCode() > 0 {
			headers := protoHeadersToHTTPHeader(bridgeResp.GetHeaders())
			for k, v := range headers {
				w.Header()[k] = v
			}
			w.WriteHeader(int(bridgeResp.GetStatusCode()))
			if len(bridgeResp.GetPayload()) > 0 {
				_, _ = w.Write(bridgeResp.GetPayload())
			}
			return
		}
		closeConnection(w)
		return
	}

	headers := protoHeadersToHTTPHeader(bridgeResp.GetHeaders())
	writeSubscriptionResponse(w, r, headers, bridgeResp.GetPayload())
}

func (a *App) returnWebpage(clientIP, shortUUID string, w http.ResponseWriter, r *http.Request) {
	subscriptionDataRaw, err := a.requestJSON(r.Context(), &proto.SubscriptionBridgeRequest{
		Operation: bridgeOperationSubscriptionInfo,
		ShortUuid: shortUUID,
		ClientIp:  clientIP,
	})
	if err != nil {
		logger.WithContext("RootService").Errorf("Error in GetSubscriptionInfo Request: %v", err)
		closeConnection(w)
		return
	}

	subpageEnvelopeRaw, err := a.requestJSON(r.Context(), &proto.SubscriptionBridgeRequest{
		Operation: bridgeOperationSubpageByShortUUID,
		ShortUuid: shortUUID,
		Headers:   filterAndConvertToProtoHeaders(r.Header),
	})
	if err != nil {
		logger.WithContext("RootService").Errorf("Error in GetSubpageConfig Request: %v", err)
		closeConnection(w)
		return
	}

	var subscriptionData map[string]any
	if err := json.Unmarshal(subscriptionDataRaw, &subscriptionData); err != nil {
		logger.WithContext("RootService").Errorf("Error in returnWebpage: failed to parse subscription info: %v", err)
		closeConnection(w)
		return
	}

	var subpageEnvelope subpageConfigByShortEnvelope
	if err := json.Unmarshal(subpageEnvelopeRaw, &subpageEnvelope); err != nil {
		logger.WithContext("RootService").Errorf("Error in returnWebpage: failed to parse subpage envelope: %v", err)
		closeConnection(w)
		return
	}

	subpageConfigUUID := strings.TrimSpace(subpageEnvelope.Response.SubpageConfigUUID)
	if subpageConfigUUID == "" {
		logger.WithContext("RootService").Errorf("[FATAL] SubPage config for %s not found", shortUUID)
		closeConnection(w)
		return
	}

	if !subpageEnvelope.Response.WebpageAllowed {
		logger.WithContext("RootService").Info("Webpage access is not allowed by Exodus's SRR.")
		closeConnection(w)
		return
	}

	subpageConfigRaw, err := a.getSubpageConfigByUUID(r.Context(), subpageConfigUUID)
	if err != nil {
		logger.WithContext("SubpageConfigService").Errorf("[FATAL] SubPage config %s not found", subpageConfigUUID)
		closeConnection(w)
		return
	}

	settings := parseBaseSettings(subpageConfigRaw)
	if !settings.ShowConnectionKeys {
		hideConnectionKeys(subscriptionData)
	}

	sessionToken, err := security.SignJWT(security.SessionClaims{
		SessionID:         security.RandomToken(32),
		SubpageConfigUUID: subpageConfigUUID,
		Exp:               time.Now().Add(33 * time.Minute).Unix(),
	}, a.cfg.SessionSecret)
	if err != nil {
		logger.WithContext("RootService").Errorf("Error in returnWebpage: failed to build session jwt: %v", err)
		closeConnection(w)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionToken,
		HttpOnly: true,
		Secure:   true,
		Path:     a.cfg.Backend.WithSlash(),
		MaxAge:   1800,
	})

	panelData, err := json.Marshal(subscriptionData)
	if err != nil {
		logger.WithContext("RootService").Errorf("Error in returnWebpage: failed to marshal subscription info: %v", err)
		closeConnection(w)
		return
	}
	panelDataBase64 := base64.StdEncoding.EncodeToString(panelData)

	indexHTML, err := a.getIndexHTML()
	if err != nil {
		logger.WithContext("RootService").Errorf("Error in returnWebpage: failed to read index.html: %v", err)
		closeConnection(w)
		return
	}

	rendered := renderIndexTemplate(
		indexHTML,
		settings.MetaTitle,
		settings.MetaDescription,
		panelDataBase64,
	)
	rendered = a.prefixAssetsInHTML(rendered)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, rendered)
	}
}

func (a *App) returnIndex(w http.ResponseWriter, r *http.Request) {
	indexHTML, err := a.getIndexHTML()
	if err != nil {
		logger.WithContext("RootService").Errorf("Error in returnIndex: failed to read index.html: %v", err)
		closeConnection(w)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, indexHTML)
	}
}

func (a *App) requestJSON(ctx context.Context, req *proto.SubscriptionBridgeRequest) ([]byte, error) {
	resp, err := a.bridge.QueryPanel(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.GetStatusCode() < 200 || resp.GetStatusCode() >= 300 {
		return nil, fmt.Errorf("panel returned status %d: %s", resp.GetStatusCode(), strings.TrimSpace(resp.GetError()))
	}
	if len(resp.GetPayload()) == 0 {
		return nil, fmt.Errorf("empty panel payload")
	}
	return resp.GetPayload(), nil
}

func (a *App) getSubpageConfigByUUID(ctx context.Context, subpageConfigUUID string) ([]byte, error) {
	if cached, ok := a.bridge.GetCachedSubpageConfig(subpageConfigUUID); ok && len(cached) > 0 {
		return cached, nil
	}

	resp, err := a.requestJSON(ctx, &proto.SubscriptionBridgeRequest{
		Operation:         bridgeOperationSubpageByUUID,
		SubpageConfigUuid: subpageConfigUUID,
	})
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (a *App) verifySessionCookie(r *http.Request) (*security.SessionClaims, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, err
	}

	return security.VerifySessionJWT(cookie.Value, a.cfg.SessionSecret)
}

func (a *App) applyCustomPrefix(requestPath string) (string, bool) {
	if !a.cfg.Backend.IsCustom() {
		return requestPath, true
	}

	prefix := a.cfg.Backend.Trimmed()
	if requestPath == prefix {
		return "/", true
	}

	if strings.HasPrefix(requestPath, prefix+"/") {
		return strings.TrimPrefix(requestPath, prefix), true
	}

	return "", false
}

func (a *App) isAppConfigPath(requestPath string) bool {
	_, ok := appConfigPaths[requestPath]
	return ok
}

func (a *App) isBrowser(userAgent string) bool {
	for _, keyword := range browserKeywords {
		if strings.Contains(userAgent, keyword) {
			return true
		}
	}

	return false
}

func (a *App) isGenericPath(requestPath string) bool {
	for _, fragment := range genericPathFragments {
		if strings.Contains(requestPath, fragment) {
			return true
		}
	}

	return false
}

func (a *App) resolveShortUUID(ctx context.Context, clientIP, shortUUID string) (string, error) {
	_ = ctx
	_ = clientIP
	return shortUUID, nil
}

func hideConnectionKeys(subscriptionData map[string]any) {
	responseValue, ok := subscriptionData["response"]
	if !ok {
		return
	}

	responseMap, ok := responseValue.(map[string]any)
	if !ok {
		return
	}

	responseMap["links"] = []any{}
	responseMap["ssConfLinks"] = map[string]any{}
}

func toProtoHeaders(headers http.Header) []*proto.Header {
	if len(headers) == 0 {
		return nil
	}
	result := make([]*proto.Header, 0, len(headers))
	for key, values := range headers {
		for _, value := range values {
			result = append(result, &proto.Header{Key: key, Value: value})
		}
	}
	return result
}

func parseBaseSettings(rawConfig []byte) baseSettings {
	defaultSettings := baseSettings{
		MetaTitle:          "Subscription Page",
		MetaDescription:    "Subscription Page",
		ShowConnectionKeys: false,
	}

	var envelope baseSettingsEnvelope
	if err := json.Unmarshal(rawConfig, &envelope); err != nil {
		return defaultSettings
	}

	metaTitle := strings.TrimSpace(envelope.BaseSettings.MetaTitle)
	if metaTitle == "" {
		metaTitle = defaultSettings.MetaTitle
	}
	metaDescription := strings.TrimSpace(envelope.BaseSettings.MetaDescription)
	if metaDescription == "" {
		metaDescription = defaultSettings.MetaDescription
	}

	return baseSettings{
		MetaTitle:          metaTitle,
		MetaDescription:    metaDescription,
		ShowConnectionKeys: envelope.BaseSettings.ShowConnectionKeys,
	}
}

func writeSubscriptionResponse(w http.ResponseWriter, r *http.Request, headers http.Header, payload []byte) {
	for key, values := range headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(payload)
	}
}

func protoHeadersToHTTPHeader(headers []*proto.Header) http.Header {
	result := make(http.Header)
	for _, header := range headers {
		if header == nil {
			continue
		}
		key := strings.TrimSpace(header.GetKey())
		if key == "" {
			continue
		}
		result.Add(key, header.GetValue())
	}
	return result
}
