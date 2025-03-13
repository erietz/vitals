package main

import (
	"os"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestParseStatusRange(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    StatusRange
		wantErr bool
	}{
		{
			name:    "valid range 200-299",
			input:   "200-299",
			want:    StatusRange{Min: 200, Max: 299},
			wantErr: false,
		},
		{
			name:    "valid range 400-499",
			input:   "400-499",
			want:    StatusRange{Min: 400, Max: 499},
			wantErr: false,
		},
		{
			name:    "invalid format",
			input:   "200:299",
			wantErr: true,
		},
		{
			name:    "invalid numbers",
			input:   "abc-def",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStatusRange(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseStatusRange() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && (got.Min != tt.want.Min || got.Max != tt.want.Max) {
				t.Errorf("parseStatusRange() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsStatusAcceptable(t *testing.T) {
	tests := []struct {
		name   string
		status int
		codes  []int
		ranges []StatusRange
		want   bool
	}{
		{
			name:   "exact match",
			status: 200,
			codes:  []int{200, 201, 204},
			ranges: nil,
			want:   true,
		},
		{
			name:   "no match in codes",
			status: 400,
			codes:  []int{200, 201, 204},
			ranges: nil,
			want:   false,
		},
		{
			name:   "match in range",
			status: 203,
			codes:  nil,
			ranges: []StatusRange{{Min: 200, Max: 299}},
			want:   true,
		},
		{
			name:   "no match in range",
			status: 400,
			codes:  nil,
			ranges: []StatusRange{{Min: 200, Max: 299}},
			want:   false,
		},
		{
			name:   "match in multiple ranges",
			status: 404,
			codes:  []int{200},
			ranges: []StatusRange{
				{Min: 200, Max: 299},
				{Min: 400, Max: 499},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isStatusAcceptable(tt.status, tt.codes, tt.ranges)
			if got != tt.want {
				t.Errorf("isStatusAcceptable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigParsing(t *testing.T) {
	// Create a temporary config file
	configContent := `
[global]
timeout = 10

[targets.api1]
name = "API 1"
base_urls = ["http://api1.example.com"]
endpoints = ["/health", "/status"]
status_codes = [200, 201]
status_ranges = ["500-599"]

[targets.api2]
name = "API 2"
base_urls = ["http://api2.example.com"]
endpoints = ["/health"]
status_codes = [200]
`

	tmpfile, err := os.CreateTemp("", "vitals-test-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(configContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	// Test config parsing
	var config Config
	if _, err := toml.DecodeFile(tmpfile.Name(), &config); err != nil {
		t.Fatalf("Failed to parse config: %v", err)
	}

	// Verify parsed config
	if config.Global.Timeout != 10 {
		t.Errorf("Expected timeout 10, got %d", config.Global.Timeout)
	}

	if len(config.Targets) != 2 {
		t.Errorf("Expected 2 targets, got %d", len(config.Targets))
	}

	api1 := config.Targets["api1"]
	if api1.Name != "API 1" {
		t.Errorf("Expected name 'API 1', got %s", api1.Name)
	}
	if len(api1.BaseURLs) != 1 || api1.BaseURLs[0] != "http://api1.example.com" {
		t.Errorf("Unexpected base_urls: %v", api1.BaseURLs)
	}
	if len(api1.StatusCodes) != 2 || api1.StatusCodes[0] != 200 || api1.StatusCodes[1] != 201 {
		t.Errorf("Unexpected status_codes: %v", api1.StatusCodes)
	}
	if len(api1.StatusRanges) != 1 || api1.StatusRanges[0] != "500-599" {
		t.Errorf("Unexpected status_ranges: %v", api1.StatusRanges)
	}
}
