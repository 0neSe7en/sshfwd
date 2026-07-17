package resolver

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name:   "consul",
			config: Config{Type: "consul", Address: "127.0.0.1:8500"},
		},
		{
			name:    "missing consul address",
			config:  Config{Type: "consul"},
			wantErr: "consul address is empty",
		},
		{
			name:    "unknown resolver",
			config:  Config{Type: "dns"},
			wantErr: "unsupported resolver",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := New(test.config, "ssh")
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := got.(consulResolver); !ok {
				t.Fatalf("resolver = %T", got)
			}
		})
	}
}
