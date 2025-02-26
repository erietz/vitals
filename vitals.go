// Vitals - API Health Check Tool
package main

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fatih/color"
)

// Config represents the top-level configuration structure
type Config struct {
	Global  GlobalConfig            `toml:"global"`
	Targets map[string]TargetConfig `toml:"targets"`
}

// GlobalConfig represents global configuration settings
type GlobalConfig struct {
	Timeout int `toml:"timeout"`
}

// TargetConfig represents configuration for a specific API target
type TargetConfig struct {
	Name         string            `toml:"name"`
	BaseURLs     []string          `toml:"base_urls"`
	Endpoints    []string          `toml:"endpoints"`
	Headers      map[string]string `toml:"headers"`
	StatusCodes  []int             `toml:"status_codes"`
	StatusRanges []string          `toml:"status_ranges"`
}

// StatusRange represents a range of acceptable HTTP status codes
type StatusRange struct {
	Min int
	Max int
}

// parseStatusRange parses a status range string like "200-299"
func parseStatusRange(rangeStr string) (StatusRange, error) {
	var min, max int
	_, err := fmt.Sscanf(rangeStr, "%d-%d", &min, &max)
	if err != nil {
		return StatusRange{}, err
	}
	return StatusRange{Min: min, Max: max}, nil
}

// isStatusAcceptable checks if the status code is in the acceptable list or ranges
func isStatusAcceptable(status int, codes []int, ranges []StatusRange) bool {
	// Check if status is in the list of acceptable codes
	for _, code := range codes {
		if status == code {
			return true
		}
	}

	// Check if status is in any of the acceptable ranges
	for _, r := range ranges {
		if status >= r.Min && status <= r.Max {
			return true
		}
	}

	return false
}

func main() {
	// Read config file path from command line or use default
	configFile := "vitals.toml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	// Parse config file
	var config Config
	if _, err := toml.DecodeFile(configFile, &config); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config file: %s\n", err)
		os.Exit(1)
	}

	// Set default timeout if not specified
	timeout := 5
	if config.Global.Timeout > 0 {
		timeout = config.Global.Timeout
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}

	// Create color output
	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()

	// Process each target group
	for targetName, target := range config.Targets {
		fmt.Println("--------------------------------------------------------------------------")
		fmt.Printf("%s\n", targetName)
		fmt.Println("--------------------------------------------------------------------------")

		// Parse status ranges
		var statusRanges []StatusRange
		for _, rangeStr := range target.StatusRanges {
			r, err := parseStatusRange(rangeStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error parsing status range '%s': %s\n", rangeStr, err)
				continue
			}
			statusRanges = append(statusRanges, r)
		}

		// Default to 200 if no status codes or ranges specified
		if len(target.StatusCodes) == 0 && len(statusRanges) == 0 {
			target.StatusCodes = []int{200}
		}

		var wg sync.WaitGroup
		for _, baseURL := range target.BaseURLs {
			for _, endpoint := range target.Endpoints {
				wg.Add(1)
				go func(baseURL, endpoint string) {
					defer wg.Done()

					// Construct full URL
					url := baseURL
					if endpoint != "" {
						if endpoint[0] != '/' && baseURL[len(baseURL)-1] != '/' {
							url += "/"
						}
						url += endpoint
					}

					// Create request
					req, err := http.NewRequest("GET", url, nil)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Error creating request for %s: %s\n", url, err)
						fmt.Println(red(fmt.Sprintf("GET %s", url)))
						return
					}

					// Add headers
					for key, value := range target.Headers {
						req.Header.Add(key, value)
					}

					// Send request
					resp, err := client.Do(req)
					if err != nil {
						fmt.Println(red(fmt.Sprintf("GET %s", url)))
						return
					}
					defer resp.Body.Close()

					// Check status code
					if isStatusAcceptable(resp.StatusCode, target.StatusCodes, statusRanges) {
						fmt.Println(green(fmt.Sprintf("GET %s - %d", url, resp.StatusCode)))
					} else {
						fmt.Println(red(fmt.Sprintf("GET %s - %d", url, resp.StatusCode)))
					}
				}(baseURL, endpoint)
			}
		}
		wg.Wait()
	}
}
