package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// EnterpriseRequestFingerprint returns the only persisted representation of a
// relay request used for enterprise idempotency. D intentionally accepts JSON
// only; multipart and WebSocket requests stay outside the enterprise path.
func EnterpriseRequestFingerprint(tokenID int, method, requestPath string, body []byte) (string, error) {
	if tokenID <= 0 || len(body) == 0 || !strings.HasPrefix(requestPath, "/") {
		return "", errors.New("invalid enterprise request fingerprint input")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		return "", errors.New("invalid enterprise request method")
	}
	var value any
	if err := common.DecodeJsonUseNumber(bytes.NewReader(body), &value); err != nil {
		return "", err
	}
	normalizedBody, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	canonicalPath := path.Clean(requestPath)
	sum := sha256.Sum256([]byte(strconv.Itoa(tokenID) + "\x00" + method + "\x00" + canonicalPath + "\x00" + string(normalizedBody)))
	return hex.EncodeToString(sum[:]), nil
}
