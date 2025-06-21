package iputil

import "testing"

func TestIsValidIP(t *testing.T) {
	tests := []struct {
		ip       string
		expected bool
	}{
		{"192.168.1.1", true},
		{"203.0.113.1", true},
		{"2001:db8::1", true},
		{"::1", true},
		{"invalid-ip", false},
		{"", false},
		{"192.168.1.256", false},
		{"192.168.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			result := IsValidIP(tt.ip)
			if result != tt.expected {
				t.Errorf("IsValidIP(%s) = %v, expected %v", tt.ip, result, tt.expected)
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip       string
		expected bool
	}{
		{"192.168.1.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"127.0.0.1", true},
		{"203.0.113.1", false},
		{"8.8.8.8", false},
		{"invalid-ip", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			result := IsPrivateIP(tt.ip)
			if result != tt.expected {
				t.Errorf("IsPrivateIP(%s) = %v, expected %v", tt.ip, result, tt.expected)
			}
		})
	}
}
