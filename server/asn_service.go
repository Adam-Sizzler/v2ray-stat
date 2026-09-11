package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"exodus-node/config"

	"github.com/Adam-Sizzler/lmdb-go/lmdb"
	"github.com/klauspost/compress/zstd"
	"github.com/vmihailenco/msgpack/v5"
)

const (
	DefaultAsnLmdbPath   = "/usr/local/share/asn/asn-prefixes.lmdb"
	FallbackAsnLmdbPath  = "/var/lib/exodus-node/asn/asn-prefixes.lmdb"
	DefaultAsnReleaseURL = "https://github.com/Adam-Sizzler/lmdb-go/releases/download/latest/asn-prefixes.lmdb.zst"

	keyFmtOrderedBinary = 1
	keyFmtString        = 2
	keyFmt32BE          = 3
	keyFmt32LE          = 4
	keyFmt64BE          = 5
	keyFmt64LE          = 6
	keyFmtFloat64       = 7

	codecMsgpack = 1
	codecJSON    = 2
)

// AsnPrefixes represents the IPv4 and IPv6 subnets associated with an ASN.
type AsnPrefixes struct {
	IPv4 []string `json:"ipv4" msgpack:"ipv4"`
	IPv6 []string `json:"ipv6" msgpack:"ipv6"`
}

// AsnLmdbService provides high-performance lookup of IP prefixes by ASN number from LMDB.
type AsnLmdbService struct {
	mu                sync.RWMutex
	dbPath            string
	env               *lmdb.Env
	isAvailable       bool
	detectedKeyFormat int
	detectedCodec     int
	logger            interface {
		Log(string, ...any)
		Warn(string, ...any)
		Error(string, ...any)
		Debug(string, ...any)
		Info(string, ...any)
	}
	stopChan chan struct{}
}

// NewAsnLmdbService initializes the ASN LMDB lookup service.
func NewAsnLmdbService(cfg *config.NodeConfig) *AsnLmdbService {
	service := &AsnLmdbService{
		stopChan: make(chan struct{}),
	}
	if cfg != nil && cfg.Logger != nil {
		service.logger = cfg.LoggerFor("AsnLmdbService")
	}

	lmdbPath := os.Getenv("ASN_LMDB_PATH")
	if lmdbPath == "" {
		lmdbPath = DefaultAsnLmdbPath
	}
	service.dbPath = lmdbPath

	if err := service.openDB(); err != nil {
		// Try fallback location if primary path doesn't exist
		if service.dbPath != FallbackAsnLmdbPath {
			service.dbPath = FallbackAsnLmdbPath
			_ = service.openDB()
		}
	}

	if service.isAvailable {
		if service.logger != nil {
			service.logger.Info("ASN LMDB database opened successfully")
		}
	} else {
		if service.logger != nil {
			service.logger.Info(fmt.Sprintf("ASN LMDB database not found at %s — downloading dataset in background...", service.dbPath))
		}
	}

	go service.startPeriodicUpdater()

	return service
}

func (s *AsnLmdbService) startPeriodicUpdater() {
	// First check 5 seconds after startup, then every 24 hours
	time.Sleep(3 * time.Second)
	if err := s.CheckAndUpdateDataset(DefaultAsnReleaseURL); err != nil {
		if s.logger != nil {
			s.logger.Warn(fmt.Sprintf("Initial ASN dataset check failed: %v", err))
		}
	}

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			if err := s.CheckAndUpdateDataset(DefaultAsnReleaseURL); err != nil {
				if s.logger != nil {
					s.logger.Warn(fmt.Sprintf("Periodic ASN dataset update failed: %v", err))
				}
			}
		}
	}
}

func (s *AsnLmdbService) openDB() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	targetPath := s.dbPath
	if _, err := os.Stat(targetPath); err != nil {
		if strings.HasSuffix(targetPath, "asn-prefixes.lmdb") {
			parent := filepath.Dir(targetPath)
			if _, parentErr := os.Stat(filepath.Join(parent, "data.mdb")); parentErr == nil {
				targetPath = parent
			}
		}
	} else {
		// If path is a directory containing data.mdb, open directory
		if stat, err := os.Stat(filepath.Join(targetPath, "data.mdb")); err == nil && !stat.IsDir() {
			// directory directly contains data.mdb
		} else if stat, err := os.Stat(filepath.Join(targetPath, "asn-prefixes.lmdb", "data.mdb")); err == nil && !stat.IsDir() {
			targetPath = filepath.Join(targetPath, "asn-prefixes.lmdb")
		} else if strings.HasSuffix(targetPath, "asn-prefixes.lmdb") {
			parent := filepath.Dir(targetPath)
			if _, parentErr := os.Stat(filepath.Join(parent, "data.mdb")); parentErr == nil {
				targetPath = parent
			}
		}
	}

	stat, err := os.Stat(targetPath)
	if err != nil {
		s.isAvailable = false
		return fmt.Errorf("lmdb path does not exist: %s", targetPath)
	}

	env, err := lmdb.NewEnv()
	if err != nil {
		s.isAvailable = false
		return fmt.Errorf("create lmdb env: %w", err)
	}

	_ = env.SetMaxDBs(10)
	var flags uint = lmdb.Readonly
	if !stat.IsDir() {
		flags |= lmdb.NoSubdir
	}

	if err := env.Open(targetPath, flags, 0644); err != nil {
		// Try alternate flag mode if first open attempt failed
		altFlags := uint(lmdb.Readonly)
		if (flags & lmdb.NoSubdir) == 0 {
			altFlags |= lmdb.NoSubdir
		}
		if retryErr := env.Open(targetPath, altFlags, 0644); retryErr != nil {
			env.Close()
			s.isAvailable = false
			return fmt.Errorf("open lmdb env at %s: %w", targetPath, err)
		}
	}

	if s.env != nil {
		s.env.Close()
	}
	s.env = env
	s.isAvailable = true
	s.dbPath = targetPath
	return nil
}

// Close releases the LMDB environment resources.
func (s *AsnLmdbService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-s.stopChan:
	default:
		close(s.stopChan)
	}

	if s.env != nil {
		err := s.env.Close()
		s.env = nil
		s.isAvailable = false
		return err
	}
	return nil
}

// IsAvailable returns whether the ASN LMDB database is currently loaded.
func (s *AsnLmdbService) IsAvailable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isAvailable
}

func getKeyBytesByFormat(asn int, format int) []byte {
	switch format {
	case keyFmtOrderedBinary:
		return encodeOrderedBinaryNumberKey(uint32(asn))
	case keyFmtString:
		return []byte(strconv.Itoa(asn))
	case keyFmt32BE:
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, uint32(asn))
		return buf
	case keyFmt32LE:
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, uint32(asn))
		return buf
	case keyFmt64BE:
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, uint64(asn))
		return buf
	case keyFmt64LE:
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, uint64(asn))
		return buf
	case keyFmtFloat64:
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, math.Float64bits(float64(asn)))
		return buf
	default:
		return nil
	}
}

func (s *AsnLmdbService) lookupInTxn(txn *lmdb.Txn, dbi lmdb.DBI, asn int) (*AsnPrefixes, error) {
	if asn <= 0 {
		return nil, nil
	}

	keyFmt := s.detectedKeyFormat
	codec := s.detectedCodec

	// Fast path: if key and codec formats are already discovered
	if keyFmt > 0 && codec > 0 {
		keyBytes := getKeyBytesByFormat(asn, keyFmt)
		valBytes, err := txn.Get(dbi, keyBytes)
		if err == nil && len(valBytes) > 0 {
			var target AsnPrefixes
			var unmarshalErr error
			if codec == codecMsgpack {
				unmarshalErr = msgpack.Unmarshal(valBytes, &target)
			} else {
				unmarshalErr = json.Unmarshal(valBytes, &target)
			}
			if unmarshalErr == nil && (len(target.IPv4) > 0 || len(target.IPv6) > 0) {
				return &target, nil
			}
		}
	}

	// Fallback / discovery path: try candidate formats
	asnStr := strconv.Itoa(asn)
	buf32LE := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf32LE, uint32(asn))
	buf32BE := make([]byte, 4)
	binary.BigEndian.PutUint32(buf32BE, uint32(asn))
	buf64LE := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf64LE, uint64(asn))
	buf64BE := make([]byte, 8)
	binary.BigEndian.PutUint64(buf64BE, uint64(asn))
	bufFloat64 := make([]byte, 8)
	binary.BigEndian.PutUint64(bufFloat64, math.Float64bits(float64(asn)))

	candidates := []struct {
		format   int
		keyBytes []byte
	}{
		{keyFmtOrderedBinary, encodeOrderedBinaryNumberKey(uint32(asn))},
		{keyFmtString, []byte(asnStr)},
		{keyFmt32BE, buf32BE},
		{keyFmt32LE, buf32LE},
		{keyFmt64BE, buf64BE},
		{keyFmt64LE, buf64LE},
		{keyFmtFloat64, bufFloat64},
	}

	for _, cand := range candidates {
		valBytes, err := txn.Get(dbi, cand.keyBytes)
		if err == nil && len(valBytes) > 0 {
			var target AsnPrefixes
			if msgpack.Unmarshal(valBytes, &target) == nil && (len(target.IPv4) > 0 || len(target.IPv6) > 0) {
				s.detectedKeyFormat = cand.format
				s.detectedCodec = codecMsgpack
				return &target, nil
			}
			if json.Unmarshal(valBytes, &target) == nil && (len(target.IPv4) > 0 || len(target.IPv6) > 0) {
				s.detectedKeyFormat = cand.format
				s.detectedCodec = codecJSON
				return &target, nil
			}
			var genericMap map[string]any
			if json.Unmarshal(valBytes, &genericMap) == nil {
				parsed := parseGenericAsnMap(genericMap)
				if len(parsed.IPv4) > 0 || len(parsed.IPv6) > 0 {
					return &parsed, nil
				}
			}
			if msgpack.Unmarshal(valBytes, &genericMap) == nil {
				parsed := parseGenericAsnMap(genericMap)
				if len(parsed.IPv4) > 0 || len(parsed.IPv6) > 0 {
					return &parsed, nil
				}
			}
		}
	}

	return nil, nil
}

// GetByASN returns the AsnPrefixes entry for a given ASN number.
func (s *AsnLmdbService) GetByASN(asn int) (*AsnPrefixes, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.isAvailable || s.env == nil || asn <= 0 {
		return nil, nil
	}

	var entry *AsnPrefixes
	err := s.env.View(func(txn *lmdb.Txn) error {
		dbi, openErr := txn.OpenRoot(0)
		if openErr != nil {
			return openErr
		}
		var lookupErr error
		entry, lookupErr = s.lookupInTxn(txn, dbi, asn)
		return lookupErr
	})

	if err != nil {
		return nil, err
	}
	return entry, nil
}

func parseGenericAsnMap(m map[string]any) AsnPrefixes {
	var res AsnPrefixes
	if v4Raw, ok := m["ipv4"].([]any); ok {
		for _, item := range v4Raw {
			if s, ok := item.(string); ok && s != "" {
				res.IPv4 = append(res.IPv4, s)
			}
		}
	}
	if v6Raw, ok := m["ipv6"].([]any); ok {
		for _, item := range v6Raw {
			if s, ok := item.(string); ok && s != "" {
				res.IPv6 = append(res.IPv6, s)
			}
		}
	}
	return res
}

// ResolvePrefixes returns IPv4 and IPv6 prefixes for a given ASN.
func (s *AsnLmdbService) ResolvePrefixes(asn int) (ipv4, ipv6 []string) {
	entry, err := s.GetByASN(asn)
	if err != nil || entry == nil {
		return nil, nil
	}
	return entry.IPv4, entry.IPv6
}

// ResolveASNs resolves a list of ASNs into deduplicated and sorted lists of IPv4 and IPv6 CIDRs.
// Executes in a single LMDB view transaction for high throughput.
func (s *AsnLmdbService) ResolveASNs(asns []int) (ipv4, ipv6 []string) {
	if len(asns) == 0 {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.isAvailable || s.env == nil {
		return nil, nil
	}

	seenV4 := make(map[string]struct{})
	seenV6 := make(map[string]struct{})

	_ = s.env.View(func(txn *lmdb.Txn) error {
		dbi, err := txn.OpenRoot(0)
		if err != nil {
			return err
		}
		for _, asn := range asns {
			entry, lookupErr := s.lookupInTxn(txn, dbi, asn)
			if lookupErr == nil && entry != nil {
				for _, cidr := range entry.IPv4 {
					seenV4[cidr] = struct{}{}
				}
				for _, cidr := range entry.IPv6 {
					seenV6[cidr] = struct{}{}
				}
			}
		}
		return nil
	})

	outV4 := make([]string, 0, len(seenV4))
	for cidr := range seenV4 {
		outV4 = append(outV4, cidr)
	}
	slices.Sort(outV4)

	outV6 := make([]string, 0, len(seenV6))
	for cidr := range seenV6 {
		outV6 = append(outV6, cidr)
	}
	slices.Sort(outV6)

	return outV4, outV6
}

// CheckAndUpdateDataset checks the remote ASN dataset against local database SHA-256 hash.
// If the hashes differ or local database is missing, it downloads, extracts, and reloads LMDB in runtime.
func (s *AsnLmdbService) CheckAndUpdateDataset(urlStr string) error {
	if urlStr == "" {
		urlStr = DefaultAsnReleaseURL
	}

	targetDir := s.dbPath
	if stat, err := os.Stat(targetDir); err == nil && !stat.IsDir() {
		targetDir = filepath.Dir(targetDir)
	} else if strings.HasSuffix(targetDir, "asn-prefixes.lmdb") {
		targetDir = filepath.Dir(targetDir)
	}

	localDataFile := s.dbPath
	if stat, err := os.Stat(localDataFile); err == nil && stat.IsDir() {
		if _, dataErr := os.Stat(filepath.Join(localDataFile, "data.mdb")); dataErr == nil {
			localDataFile = filepath.Join(localDataFile, "data.mdb")
		}
	} else if _, err := os.Stat(localDataFile); err != nil {
		if _, altErr := os.Stat(filepath.Join(targetDir, "asn-prefixes.lmdb")); altErr == nil {
			localDataFile = filepath.Join(targetDir, "asn-prefixes.lmdb")
		} else if _, dataErr := os.Stat(filepath.Join(targetDir, "data.mdb")); dataErr == nil {
			localDataFile = filepath.Join(targetDir, "data.mdb")
		}
	}

	localHash, _ := computeFileSha256(localDataFile)

	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		return fmt.Errorf("create http request: %w", err)
	}
	req.Header.Set("User-Agent", "exodus-node/asn-updater")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http get %s failed: %w", urlStr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http get %s returned status %d", urlStr, resp.StatusCode)
	}

	archiveBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	var remoteDataBytes []byte
	filesToExtract := make(map[string][]byte)

	if strings.HasSuffix(urlStr, ".zst") {
		decoder, err := zstd.NewReader(bytes.NewReader(archiveBytes))
		if err != nil {
			return fmt.Errorf("create zstd reader: %w", err)
		}
		defer decoder.Close()

		decompressed, err := io.ReadAll(decoder)
		if err != nil {
			return fmt.Errorf("decompress zstd stream: %w", err)
		}
		remoteDataBytes = decompressed
		filesToExtract["asn-prefixes.lmdb"] = decompressed
	} else {
		// Unpack in memory to check data.mdb hash
		gr, err := gzip.NewReader(bytes.NewReader(archiveBytes))
		if err != nil {
			return fmt.Errorf("read gzip stream: %w", err)
		}
		defer gr.Close()

		tr := tar.NewReader(gr)
		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("read tar entry: %w", err)
			}
			if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
				content, err := io.ReadAll(tr)
				if err != nil {
					return fmt.Errorf("read tar file %s: %w", header.Name, err)
				}
				filesToExtract[header.Name] = content
				if filepath.Base(header.Name) == "data.mdb" || strings.HasSuffix(header.Name, ".lmdb") {
					remoteDataBytes = content
				}
			}
		}
	}

	if len(remoteDataBytes) == 0 {
		return fmt.Errorf("remote archive does not contain valid LMDB data")
	}

	remoteHashSum := sha256.Sum256(remoteDataBytes)
	remoteHash := hex.EncodeToString(remoteHashSum[:])

	if s.isAvailable && localHash != "" && localHash == remoteHash {
		if s.logger != nil {
			preview := localHash
			if len(preview) > 12 {
				preview = preview[:12]
			}
			s.logger.Info("ASN dataset check: database is up to date (sha256: " + preview + "...)")
		}
		return nil
	}

	// Update required
	if s.logger != nil {
		localPreview := localHash
		if len(localPreview) > 12 {
			localPreview = localPreview[:12]
		} else if localPreview == "" {
			localPreview = "none"
		}
		remotePreview := remoteHash
		if len(remotePreview) > 12 {
			remotePreview = remotePreview[:12]
		}
		s.logger.Info(fmt.Sprintf("New ASN dataset detected (remote: %s..., local: %s...), updating database...", remotePreview, localPreview))
	}

	lmdbDir := filepath.Join(targetDir, "asn-prefixes.lmdb")
	_ = os.MkdirAll(targetDir, 0755)

	for name, content := range filesToExtract {
		var targetPath string
		if name == "asn-prefixes.lmdb" {
			targetPath = filepath.Join(targetDir, name)
		} else if strings.HasPrefix(name, "asn-prefixes.lmdb/") {
			targetPath = filepath.Join(targetDir, name)
		} else {
			_ = os.MkdirAll(lmdbDir, 0755)
			targetPath = filepath.Join(lmdbDir, filepath.Base(name))
		}
		_ = os.MkdirAll(filepath.Dir(targetPath), 0755)
		if err := os.WriteFile(targetPath, content, 0644); err != nil {
			return fmt.Errorf("write extracted file %s: %w", targetPath, err)
		}
	}

	// Re-open LMDB database
	if err := s.openDB(); err != nil {
		return fmt.Errorf("re-open updated database: %w", err)
	}

	if s.logger != nil {
		remotePreview := remoteHash
		if len(remotePreview) > 12 {
			remotePreview = remotePreview[:12]
		}
		s.logger.Info("[OK] ASN LMDB database updated successfully (sha256: " + remotePreview + "...)")
	}

	return nil
}

func computeFileSha256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// FetchAndLoadDataset downloads and extracts the daily ASN dataset archive if missing or requested.
func (s *AsnLmdbService) FetchAndLoadDataset(urlStr string) error {
	return s.CheckAndUpdateDataset(urlStr)
}

func encodeOrderedBinaryNumberKey(n uint32) []byte {
	f := float64(n)
	bits := math.Float64bits(f)
	highInt, lowInt := uint32(bits>>32), uint32(bits)

	var length int
	if (lowInt & 0xf) != 0 {
		length = 9
	} else if (lowInt & 0xfffff) != 0 {
		length = 8
	} else if lowInt != 0 || (highInt&0xf) != 0 {
		length = 6
	} else {
		length = 4
	}

	b0_3 := (highInt >> 4) | 0x10000000
	b4_7 := (lowInt >> 4) | (highInt << 28)
	b8 := uint8((lowInt & 0xf) << 4)

	buf := make([]byte, 9)
	binary.BigEndian.PutUint32(buf[0:4], b0_3)
	binary.BigEndian.PutUint32(buf[4:8], b4_7)
	buf[8] = b8

	return buf[:length]
}

func uint32ToBytes(n uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, n)
	return buf
}
