package application

import "testing"

func TestValidateLoopbackAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{name: "default IPv4", address: "127.0.0.1:11872"},
		{name: "explicit IPv6", address: "[::1]:11872"},
		{name: "unspecified IPv4", address: "0.0.0.0:11872", wantErr: true},
		{name: "unspecified IPv6", address: "[::]:11872", wantErr: true},
		{name: "hostname", address: "localhost:11872", wantErr: true},
		{name: "missing port", address: "127.0.0.1", wantErr: true},
		{name: "invalid port", address: "127.0.0.1:70000", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateLoopbackAddress(test.address)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateLoopbackAddress(%q) error = %v, wantErr %v", test.address, err, test.wantErr)
			}
		})
	}
}
