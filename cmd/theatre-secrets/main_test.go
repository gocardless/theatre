package main

import (
	"strings"
	"testing"

	"github.com/hashicorp/vault/api"
)

func TestExtractSecretValue(t *testing.T) {
	testCases := []struct {
		name          string
		resp          *api.Secret
		path          string
		expectedValue string
		expectedError string
	}{
		{
			name: "valid KV-v2 secret",
			resp: &api.Secret{
				Data: map[string]interface{}{
					"data": map[string]interface{}{
						"data": "super-secret-token",
					},
				},
			},
			path:          "secret/data/kubernetes/staging/app/token",
			expectedValue: "super-secret-token",
		},
		{
			name:          "nil secret response",
			resp:          nil,
			path:          "secret/data/kubernetes/staging/app/token",
			expectedError: "no secret data found at Vault KV path: secret/data/kubernetes/staging/app/token",
		},
		{
			name: "nil secret Data map",
			resp: &api.Secret{
				Data: nil,
			},
			path:          "secret/data/kubernetes/staging/app/token",
			expectedError: "no secret data found at Vault KV path: secret/data/kubernetes/staging/app/token",
		},
		{
			name: "KV-v1 response (missing outer data field)",
			resp: &api.Secret{
				Data: map[string]interface{}{
					"foo": "bar",
				},
			},
			path:          "secret/data/kubernetes/staging/app/token",
			expectedError: "unexpected response structure (KV v1 or non-KV engine?)",
		},
		{
			name: "KV-v2 deleted secret version (outer data is nil)",
			resp: &api.Secret{
				Data: map[string]interface{}{
					"data": nil,
				},
			},
			path:          "secret/data/kubernetes/staging/app/token",
			expectedError: "unexpected response structure (KV v1 or non-KV engine?)",
		},
		{
			name: "outer data is not a map",
			resp: &api.Secret{
				Data: map[string]interface{}{
					"data": "invalid-outer-type",
				},
			},
			path:          "secret/data/kubernetes/staging/app/token",
			expectedError: "unexpected response structure (KV v1 or non-KV engine?)",
		},
		{
			name: "missing inner data field (e.g. key named password)",
			resp: &api.Secret{
				Data: map[string]interface{}{
					"data": map[string]interface{}{
						"password": "secret-value",
					},
				},
			},
			path:          "secret/data/kubernetes/staging/app/token",
			expectedError: "missing inner 'data' field",
		},
		{
			name: "inner data is a map instead of a string",
			resp: &api.Secret{
				Data: map[string]interface{}{
					"data": map[string]interface{}{
						"data": map[string]interface{}{
							"nested": "val",
						},
					},
				},
			},
			path:          "secret/data/kubernetes/staging/app/token",
			expectedError: "data field is not a string (got map[string]interface {})",
		},
		{
			name: "inner data is an int instead of a string",
			resp: &api.Secret{
				Data: map[string]interface{}{
					"data": map[string]interface{}{
						"data": 12345,
					},
				},
			},
			path:          "secret/data/kubernetes/staging/app/token",
			expectedError: "data field is not a string (got int)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			val, err := extractSecretValue(tc.resp, tc.path)

			if tc.expectedError != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, but got nil", tc.expectedError)
				}
				if !strings.Contains(err.Error(), tc.expectedError) {
					t.Fatalf("expected error containing %q, but got %q", tc.expectedError, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if val != tc.expectedValue {
					t.Fatalf("expected value %q, but got %q", tc.expectedValue, val)
				}
			}
		})
	}
}
