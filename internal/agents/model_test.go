package agents

import "testing"

func TestResolveAnthropicAuthToken(t *testing.T) {
	tests := []struct {
		name      string
		authToken string
		baseURL   string
		apiKey    string
		want      string
	}{
		{
			name:   "direct anthropic keeps x-api-key only",
			apiKey: "sk-ant-123",
			want:   "",
		},
		{
			name:    "gateway with only api key reuses it as bearer",
			baseURL: "https://llm-gateway.clio.systems",
			apiKey:  "clio_g_abc",
			want:    "clio_g_abc",
		},
		{
			name:      "explicit auth token always wins",
			authToken: "bearer-xyz",
			baseURL:   "https://llm-gateway.clio.systems",
			apiKey:    "clio_g_abc",
			want:      "bearer-xyz",
		},
		{
			name:      "explicit auth token without base url",
			authToken: "bearer-xyz",
			want:      "bearer-xyz",
		},
		{
			name:    "gateway without any key yields nothing",
			baseURL: "https://llm-gateway.clio.systems",
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAnthropicAuthToken(tt.authToken, tt.baseURL, tt.apiKey)
			if got != tt.want {
				t.Errorf("resolveAnthropicAuthToken(%q, %q, %q) = %q, want %q",
					tt.authToken, tt.baseURL, tt.apiKey, got, tt.want)
			}
		})
	}
}
