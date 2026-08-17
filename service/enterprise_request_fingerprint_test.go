package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnterpriseRequestFingerprintCanonicalizesJSONWithoutPersistingBody(t *testing.T) {
	first, err := EnterpriseRequestFingerprint(42, "post", "/v1/chat/completions", []byte(`{"model":"m","stream":false,"messages":[{"content":"hi","role":"user"}]}`))
	require.NoError(t, err)
	second, err := EnterpriseRequestFingerprint(42, "POST", "/v1/chat/completions", []byte("{\n  \"messages\": [ { \"role\": \"user\", \"content\": \"hi\" } ], \"stream\": false, \"model\": \"m\"\n}"))
	require.NoError(t, err)
	assert.Len(t, first, 64)
	assert.Equal(t, first, second)

	different, err := EnterpriseRequestFingerprint(42, "POST", "/v1/chat/completions", []byte(`{"model":"m","stream":true,"messages":[{"content":"hi","role":"user"}]}`))
	require.NoError(t, err)
	assert.NotEqual(t, first, different)
}

func TestEnterpriseRequestFingerprintPreservesLargeJSONIntegers(t *testing.T) {
	first, err := EnterpriseRequestFingerprint(42, "POST", "/v1/chat/completions", []byte(`{"model":"m","seed":9007199254740993}`))
	require.NoError(t, err)
	second, err := EnterpriseRequestFingerprint(42, "POST", "/v1/chat/completions", []byte(`{"seed":9007199254740992,"model":"m"}`))
	require.NoError(t, err)
	assert.NotEqual(t, first, second)

	equivalent, err := EnterpriseRequestFingerprint(42, "POST", "/v1/chat/completions", []byte("{\n  \"seed\": 9007199254740993, \"model\": \"m\"\n}"))
	require.NoError(t, err)
	assert.Equal(t, first, equivalent)
}

func TestEnterpriseUsageReplayHTTPStatus(t *testing.T) {
	assert.Equal(t, 202, EnterpriseUsageReplayHTTPStatus("PENDING"))
	assert.Equal(t, 202, EnterpriseUsageReplayHTTPStatus("UPSTREAM_SUBMITTED"))
	assert.Equal(t, 409, EnterpriseUsageReplayHTTPStatus("MANUAL_REVIEW"))
	assert.Equal(t, 200, EnterpriseUsageReplayHTTPStatus("SETTLED"))
}
