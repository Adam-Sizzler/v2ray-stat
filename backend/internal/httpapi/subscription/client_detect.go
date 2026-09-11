package subscription

import (
	"net/http"
	"regexp"
	"strings"

	"exodus/internal/util"

	"github.com/google/uuid"
)

var hwidHeaderRegex = regexp.MustCompile(`^[a-zA-Z0-9=-]{10,64}$`)

func extractHwidHeaders(r *http.Request) *HwidHeaders {
	hwid := firstNonEmptyHeaderValue(r, "X-HWID", "X-Hwid", "Hwid", "X-HWID-Device-ID")
	if hwid == nil || !hwidHeaderRegex.MatchString(*hwid) {
		return nil
	}
	userAgent := firstNonEmptyHeader(r, "User-Agent", "X-HWID-User-Agent")
	platform := firstNonEmptyLowerHeader(r, "X-Device-OS", "X-HWID-Platform", "X-Hwid-Platform", "Hwid-Platform")
	osVersion := firstNonEmptyHeader(r, "X-Ver-OS", "X-HWID-OS-Version", "X-Hwid-Os-Version", "Hwid-Os-Version")
	deviceModel := firstNonEmptyHeader(r, "X-Device-Model", "X-HWID-Device-Model", "X-Hwid-Device-Model", "Hwid-Device-Model")
	platform, osVersion, deviceModel, userAgent = normalizeHwidMetadata(platform, osVersion, deviceModel, userAgent)

	h := &HwidHeaders{
		Hwid:        *hwid,
		Platform:    platform,
		OsVersion:   osVersion,
		DeviceModel: deviceModel,
		UserAgent:   userAgent,
	}
	return h
}

func firstNonEmptyHeaderValue(r *http.Request, names ...string) *string {
	for _, name := range names {
		val := strings.TrimSpace(r.Header.Get(name))
		if val != "" {
			return &val
		}
	}
	return nil
}

func extractSyntheticHwidHeaders(r *http.Request, userUUID, requestIP string) *HwidHeaders {
	userAgent := strings.TrimSpace(r.Header.Get("User-Agent"))
	platform := firstNonEmptyLowerHeader(r, "X-Device-OS", "X-HWID-Platform")
	osVersion := firstNonEmptyHeader(r, "X-Ver-OS", "X-HWID-OS-Version")
	deviceModel := firstNonEmptyHeader(r, "X-Device-Model", "X-HWID-Device-Model")

	hasMetadata := userAgent != "" || platform != nil || osVersion != nil || deviceModel != nil
	if !hasMetadata {
		return nil
	}
	platform, osVersion, deviceModel, userAgentPtr := normalizeHwidMetadata(
		platform,
		osVersion,
		deviceModel,
		stringPtrIfNotEmpty(userAgent),
	)

	var b strings.Builder
	b.Grow(128)
	b.WriteString("exodus:synthetic-hwid:v1|ua=")
	writeLowerString(&b, ptrString(userAgentPtr))
	b.WriteString("|platform=")
	writeLowerString(&b, ptrString(platform))
	b.WriteString("|os=")
	writeLowerString(&b, ptrString(osVersion))
	b.WriteString("|model=")
	writeLowerString(&b, ptrString(deviceModel))
	signature := b.String()

	return &HwidHeaders{
		Hwid:        deterministicSyntheticHwid(userUUID, signature),
		Platform:    platform,
		OsVersion:   osVersion,
		DeviceModel: deviceModel,
		UserAgent:   userAgentPtr,
		RequestIP:   stringPtrIfNotEmpty(requestIP),
		Synthetic:   true,
	}
}

func normalizeHwidMetadata(platform, osVersion, deviceModel, userAgent *string) (*string, *string, *string, *string) {
	normalizedUserAgent := stringPtrIfNotEmpty(ptrString(userAgent))
	normalizedPlatform := lowerStringPtr(platform)
	if normalizedPlatform == nil {
		if inferred := inferPlatformFromUserAgent(ptrString(normalizedUserAgent)); inferred != "" {
			normalizedPlatform = &inferred
		}
	}
	if deviceModel == nil {
		deviceModel = stringPtrIfNotEmpty("unknown")
	}
	return normalizedPlatform, stringPtrIfNotEmpty(ptrString(osVersion)), stringPtrIfNotEmpty(ptrString(deviceModel)), normalizedUserAgent
}

func writeLowerString(b *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
}

func deterministicSyntheticHwid(userUUID, signature string) string {
	namespace, err := uuid.Parse(strings.TrimSpace(userUUID))
	if err != nil {
		namespace = uuid.NameSpaceOID
	}
	return uuid.NewSHA1(namespace, []byte(signature)).String()
}

func firstNonEmptyHeader(r *http.Request, names ...string) *string {
	for _, name := range names {
		value := strings.TrimSpace(r.Header.Get(name))
		if value == "" {
			continue
		}
		return &value
	}
	return nil
}

func firstNonEmptyLowerHeader(r *http.Request, names ...string) *string {
	value := firstNonEmptyHeader(r, names...)
	if value == nil {
		return nil
	}
	return lowerStringPtr(value)
}

func stringPtrIfNotEmpty(value string) *string {
	return util.StringPtrIfNotEmpty(value)
}

func lowerStringPtr(value *string) *string {
	return util.LowerStringPtr(value)
}

func ptrString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

var userAgentSeparators = [...]string{"/", " ", "(", ";"}

func inferClientAppFromUserAgent(userAgent string) string {
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		return ""
	}
	for _, sep := range userAgentSeparators {
		if idx := strings.Index(userAgent, sep); idx > 0 {
			return strings.TrimSpace(userAgent[:idx])
		}
	}
	return userAgent
}

var (
	knownAndroidClients = []string{
		"sfa",
		"sfatv",
		"sfandroidtv",
		"v2rayng",
		"exclave",
		"nekoboxforandroid",
		"matsuri",
		"sagernet",
		"clashforandroid",
		"clashmetaforandroid",
		"cmfa",
	}

	knownIOSClients = []string{
		"sfi",
		"streisand",
		"v2box",
		"rabbithole",
		"shadowrocket",
		"loon",
		"quantumult",
		"stash",
		"choc",
	}

	knownTVOSClients = []string{
		"sft",
	}

	knownWindowsClients = []string{
		"sfw",
		"v2rayn",
	}

	knownMacOSClients = []string{
		"sfm",
		"v2rayu",
		"v2rayx",
		"v2rayxs",
		"clashx",
	}

	knownLinuxClients = []string{
		"sfl",
	}
)

func inferKnownClientPlatform(lowerUA string) string {
	for _, app := range knownAndroidClients {
		if strings.Contains(lowerUA, app) {
			return "android"
		}
	}

	for _, app := range knownIOSClients {
		if strings.Contains(lowerUA, app) {
			return "ios"
		}
	}

	for _, app := range knownTVOSClients {
		if strings.Contains(lowerUA, app) {
			return "tvos"
		}
	}

	for _, app := range knownWindowsClients {
		if strings.Contains(lowerUA, app) {
			return "windows"
		}
	}

	for _, app := range knownMacOSClients {
		if strings.Contains(lowerUA, app) {
			return "macos"
		}
	}

	for _, app := range knownLinuxClients {
		if strings.Contains(lowerUA, app) {
			return "linux"
		}
	}

	return ""
}

func inferPlatformFromUserAgent(userAgent string) string {
	lower := strings.ToLower(strings.TrimSpace(userAgent))
	if lower == "" {
		return ""
	}

	if platform := inferKnownClientPlatform(lower); platform != "" {
		return platform
	}

	if idx := strings.Index(lower, "platform/"); idx >= 0 {
		rest := lower[idx+len("platform/"):]
		for i, r := range rest {
			if !(r == '-' || r == '_' || r == '.' || r == '/' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z') {
				rest = rest[:i]
				break
			}
		}
		if rest = strings.Trim(rest, "/ "); rest != "" {
			return rest
		}
	}

	switch {
	case strings.Contains(lower, "windows"):
		return "windows"
	case strings.Contains(lower, "android"):
		return "android"
	case strings.Contains(lower, "iphone") || strings.Contains(lower, "ipad") || strings.Contains(lower, "ios"):
		return "ios"
	case strings.Contains(lower, "mac os") || strings.Contains(lower, "macos") || strings.Contains(lower, "macintosh") || strings.Contains(lower, "darwin"):
		return "macos"
	case strings.Contains(lower, "linux"):
		return "linux"
	default:
		return ""
	}
}
