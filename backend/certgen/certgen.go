package certgen

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"v2ray-stat/backend/config"
)

// EnsureCertificates проверяет наличие сертификатов и генерирует их, если они отсутствуют.
// Возвращает пути к сертификату и ключу.
func EnsureCertificates(cfg *config.Config) (certPath, keyPath string, err error) {
	// Динамически определяем директорию certs относительно текущей рабочей директории
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("failed to get current working directory: %v", err)
	}
	certsDir := filepath.Join(cwd, "certs")
	certPath = filepath.Join(certsDir, "node.crt")
	keyPath = filepath.Join(certsDir, "node.key")

	// Создаем директорию certs, если она не существует
	if err := os.MkdirAll(certsDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create certs directory %s: %v", certsDir, err)
	}
	cfg.Logger.Debug("Ensured certs directory", "path", certsDir)

	// Проверяем, существует ли сертификат, и генерируем, если отсутствует
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		cfg.Logger.Info("Certificate not found, generating new self-signed certificate", "certPath", certPath, "keyPath", keyPath)
		if err := GenerateSelfSignedCert(certPath, keyPath, cfg); err != nil {
			return "", "", fmt.Errorf("failed to generate cert: %v", err)
		}
		cfg.Logger.Info("Generated self-signed certificate", "certPath", certPath, "keyPath", keyPath)
	} else if err != nil {
		return "", "", fmt.Errorf("failed to check certificate existence: %v", err)
	} else {
		cfg.Logger.Debug("Certificate already exists", "certPath", certPath, "keyPath", keyPath)
	}

	return certPath, keyPath, nil
}

// GenerateSelfSignedCert генерирует один self-signed сертификат (общий для всех нод).
// Без SAN, без hostname. Cert может использоваться как CA (self-signed).
// Сохраняет в файлы certPath и keyPath.
func GenerateSelfSignedCert(certPath, keyPath string, cfg *config.Config) error {
	cfg.Logger.Debug("Generating private key")
	// Генерация приватного ключа
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		cfg.Logger.Error("Failed to generate private key", "error", err)
		return fmt.Errorf("failed to generate private key: %v", err)
	}

	// Создание шаблона сертификата (self-signed CA)
	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour) // 1 год
	cfg.Logger.Debug("Generating serial number")
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		cfg.Logger.Error("Failed to generate serial number", "error", err)
		return fmt.Errorf("failed to generate serial number: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"xAI Node Cert"}, // Обезличенный subject
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true, // Self-signed CA
	}

	// Создание cert (самоподписанный)
	cfg.Logger.Debug("Creating certificate")
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		cfg.Logger.Error("Failed to create certificate", "error", err)
		return fmt.Errorf("failed to create certificate: %v", err)
	}

	// Сохранение cert в PEM
	cfg.Logger.Debug("Writing certificate to file", "path", certPath)
	certOut, err := os.Create(certPath)
	if err != nil {
		cfg.Logger.Error("Failed to open certificate file for writing", "path", certPath, "error", err)
		return fmt.Errorf("failed to open %s for writing: %v", certPath, err)
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		cfg.Logger.Error("Failed to write certificate data", "path", certPath, "error", err)
		return fmt.Errorf("failed to write data to %s: %v", certPath, err)
	}

	// Сохранение key в PEM
	cfg.Logger.Debug("Writing private key to file", "path", keyPath)
	keyOut, err := os.Create(keyPath)
	if err != nil {
		cfg.Logger.Error("Failed to open private key file for writing", "path", keyPath, "error", err)
		return fmt.Errorf("failed to open %s for writing: %v", keyPath, err)
	}
	defer keyOut.Close()
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}); err != nil {
		cfg.Logger.Error("Failed to write private key data", "path", keyPath, "error", err)
		return fmt.Errorf("failed to write data to %s: %v", keyPath, err)
	}

	cfg.Logger.Info("Successfully generated self-signed certificate", "certPath", certPath, "keyPath", keyPath)
	return nil
}
