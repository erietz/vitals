// Vitals - API Health Check Tool
package main

import (
	"fmt"
	"net/http"
	"os"
	"sort"
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

// RequestStatus represents the status of a request
type RequestStatus struct {
	URL        string
	StatusCode int
	IsComplete bool
	IsSuccess  bool
	Error      error
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

	// Create color output functions
	green := color.New(color.FgGreen).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()

	// Process each target group
	isFirstTarget := true
	for _, targetConfig := range config.Targets {
		// Only print newline separator after the first target
		if !isFirstTarget {
			fmt.Println()
		}
		isFirstTarget = false

		fmt.Println("--------------------------------------------------------------------------")
		fmt.Printf("%s\n", targetConfig.Name)
		fmt.Println("--------------------------------------------------------------------------")

		// Parse status ranges
		var statusRanges []StatusRange
		for _, rangeStr := range targetConfig.StatusRanges {
			r, err := parseStatusRange(rangeStr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error parsing status range '%s': %s\n", rangeStr, err)
				continue
			}
			statusRanges = append(statusRanges, r)
		}

		// Default to 200 if no status codes or ranges specified
		if len(targetConfig.StatusCodes) == 0 && len(statusRanges) == 0 {
			targetConfig.StatusCodes = []int{200}
		}

		// Generate all URLs
		var urls []string
		uniqueUrls := make(map[string]bool)

		for _, baseURL := range targetConfig.BaseURLs {
			for _, endpoint := range targetConfig.Endpoints {
				// Construct full URL
				url := baseURL
				if endpoint != "" {
					if endpoint[0] != '/' && baseURL[len(baseURL)-1] != '/' {
						url += "/"
					}
					url += endpoint
				}

				// Only add unique URLs
				if !uniqueUrls[url] {
					urls = append(urls, url)
					uniqueUrls[url] = true
				}
			}
		}

		// Sort URLs for consistent display
		sort.Strings(urls)

		// Tracking for completions
		var wg sync.WaitGroup
		results := make(map[string]*RequestStatus)
		var resultsMutex sync.Mutex

		// Spinner chars for animation
		spinnerChars := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		var spinnerIdx int
		spinnerTicker := time.NewTicker(100 * time.Millisecond)
		defer spinnerTicker.Stop()

		// Map to track the line number for each URL
		lineMap := make(map[string]int)

		// Print initial state for all URLs
		for i, url := range urls {
			lineMap[url] = i
			fmt.Printf("%s GET %s\n", yellow(spinnerChars[0]), url)

			// Initialize results
			results[url] = &RequestStatus{
				URL:        url,
				IsComplete: false,
			}
		}

		// Start requests
		for _, url := range urls {
			wg.Add(1)

			// Create a copy of url for the goroutine
			urlCopy := url

			go func() {
				defer wg.Done()

				// Create request
				req, err := http.NewRequest("GET", urlCopy, nil)
				if err != nil {
					resultsMutex.Lock()
					results[urlCopy].IsComplete = true
					results[urlCopy].Error = err
					resultsMutex.Unlock()
					return
				}

				// Add headers
				for key, value := range targetConfig.Headers {
					req.Header.Add(key, value)
				}

				// Send request
				resp, err := client.Do(req)

				resultsMutex.Lock()
				if err != nil {
					results[urlCopy].IsComplete = true
					results[urlCopy].Error = err
				} else {
					defer resp.Body.Close()
					results[urlCopy].IsComplete = true
					results[urlCopy].StatusCode = resp.StatusCode
					results[urlCopy].IsSuccess = isStatusAcceptable(resp.StatusCode, targetConfig.StatusCodes, statusRanges)
				}
				resultsMutex.Unlock()
			}()
		}

		// Channel to signal when all requests are done
		done := make(chan struct{})

		// Start a goroutine to monitor for completion
		go func() {
			wg.Wait()
			close(done)
		}()

		// Map to track which URLs have been updated with final status
		updated := make(map[string]bool)

		// Update spinner and results until all requests complete
		lineCount := len(urls)
	updateLoop:
		for {
			select {
			case <-spinnerTicker.C:
				// Update spinner index
				spinnerIdx = (spinnerIdx + 1) % len(spinnerChars)

				resultsMutex.Lock()

				// Update display for each URL
				for _, url := range urls {
					// Skip if already updated with final status
					if updated[url] {
						continue
					}

					// Get line number and status
					lineNum := lineMap[url]
					status := results[url]

					// Move cursor to correct line
					// Need to move up (lineCount - lineNum - 1) lines from current position
					moveUp := lineCount - lineNum - 1
					if moveUp > 0 {
						fmt.Printf("\033[%dA", moveUp)
					}

					// Clear line
					fmt.Print("\r\033[K")

					// Update content
					if status.IsComplete {
						// Show final status
						if status.Error != nil {
							fmt.Printf("%s", red(fmt.Sprintf("✗ GET %s - Error: %v", url, status.Error)))
						} else if status.IsSuccess {
							fmt.Printf("%s", green(fmt.Sprintf("✓ GET %s - %d", url, status.StatusCode)))
						} else {
							fmt.Printf("%s", red(fmt.Sprintf("✗ GET %s - %d", url, status.StatusCode)))
						}
						updated[url] = true
					} else {
						// Show spinner
						fmt.Printf("%s GET %s", yellow(spinnerChars[spinnerIdx]), url)
					}

					// Move back down
					if moveUp > 0 {
						fmt.Printf("\033[%dB", moveUp)
					}
				}

				resultsMutex.Unlock()

				// Move cursor to beginning of current line
				fmt.Print("\r")

			case <-done:
				// All requests have completed

				// Make sure all URLs have their final status displayed
				resultsMutex.Lock()
				for _, url := range urls {
					if !updated[url] {
						// Get line number and status
						lineNum := lineMap[url]
						status := results[url]

						// Move cursor to correct line
						moveUp := lineCount - lineNum - 1
						if moveUp > 0 {
							fmt.Printf("\033[%dA", moveUp)

						}

						// Clear line
						fmt.Print("\r\033[K")

						// Show final status
						if status.Error != nil {
							fmt.Printf("%s", red(fmt.Sprintf("✗ GET %s - Error: %v", url, status.Error)))
						} else if status.IsSuccess {
							fmt.Printf("%s", green(fmt.Sprintf("✓ GET %s - %d", url, status.StatusCode)))
						} else {
							fmt.Printf("%s", red(fmt.Sprintf("✗ GET %s - %d", url, status.StatusCode)))
						}

						// Move back down
						if moveUp > 0 {
							fmt.Printf("\033[%dB", moveUp)
						}
					}
				}
				resultsMutex.Unlock()

				break updateLoop
			}
		}
	}
}
