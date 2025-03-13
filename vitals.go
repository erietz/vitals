// Vitals - API Health Check Tool
package main

import (
	"flag"
	"fmt"
	"io"
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

// Define CLI flags
type cliFlags struct {
	configFile  string
	timeout     int
	verbosity   bool
	concurrency int
}

// parseFlags parses command line flags
func parseFlags() cliFlags {
	flags := cliFlags{}

	flag.StringVar(&flags.configFile, "config", "vitals.toml", "Path to the configuration file")
	flag.StringVar(&flags.configFile, "c", "vitals.toml", "Path to the configuration file (shorthand)")

	flag.IntVar(&flags.timeout, "timeout", 0, "Override the global timeout in seconds")
	flag.IntVar(&flags.timeout, "t", 0, "Override the global timeout in seconds (shorthand)")

	flag.BoolVar(&flags.verbosity, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&flags.verbosity, "v", false, "Enable verbose logging (shorthand)")

	flag.IntVar(&flags.concurrency, "concurrency", 0, "Maximum number of concurrent requests (0 means unlimited)")

	// Parse the flags
	flag.Parse()

	return flags
}

// loadConfig loads and validates the configuration file
func loadConfig(configFile string) (Config, error) {
	var config Config
	if _, err := toml.DecodeFile(configFile, &config); err != nil {
		return Config{}, fmt.Errorf("error reading config file: %s", err)
	}
	return config, nil
}

// setupHTTPClient creates an HTTP client with the specified timeout
func setupHTTPClient(configTimeout, cliTimeout int) *http.Client {
	timeout := configTimeout

	// CLI timeout takes precedence if specified
	if cliTimeout > 0 {
		timeout = cliTimeout
	}

	// Default timeout if neither is specified
	if timeout <= 0 {
		timeout = 5
	}

	return &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}
}

// setupColorOutput returns colored output functions
func setupColorOutput() (func(a ...interface{}) string, func(a ...interface{}) string) {
	return color.New(color.FgGreen).SprintFunc(),
		color.New(color.FgRed).SprintFunc()
}

// EndpointResult represents the result of checking a single endpoint
type EndpointResult struct {
	URL          string
	StatusCode   int
	ResponseBody string
	Error        error
	Duration     time.Duration
	Success      bool
}

// processTarget handles checking all endpoints for a single target
func processTarget(client *http.Client, target TargetConfig, statusRanges []StatusRange, wg *sync.WaitGroup, sem chan struct{}, verbose bool) []EndpointResult {
	resultsChan := make(chan EndpointResult)
	var resultsCount int

	for _, baseURL := range target.BaseURLs {
		for _, endpoint := range target.Endpoints {
			resultsCount++
			wg.Add(1)
			go func(baseURL, endpoint string) {
				defer wg.Done()

				// If semaphore is provided, use it to limit concurrency
				if sem != nil {
					sem <- struct{}{}        // Acquire
					defer func() { <-sem }() // Release
				}

				result := checkEndpoint(client, baseURL, endpoint, target, statusRanges, verbose)
				resultsChan <- result
			}(baseURL, endpoint)
		}
	}

	// Collect results
	results := make([]EndpointResult, 0, resultsCount)
	go func() {
		for i := 0; i < resultsCount; i++ {
			results = append(results, <-resultsChan)
		}
		close(resultsChan)
	}()

	wg.Wait()
	return results
}

// checkEndpoint performs the HTTP request and checks the response
func checkEndpoint(client *http.Client, baseURL, endpoint string, target TargetConfig, statusRanges []StatusRange, verbose bool) EndpointResult {
	url := constructURL(baseURL, endpoint)

	result := EndpointResult{
		URL: url,
	}

	startTime := time.Now()

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		result.Error = fmt.Errorf("error creating request: %s", err)
		return result
	}

	// Add headers
	for key, value := range target.Headers {
		req.Header.Add(key, value)
	}

	// Send request
	if verbose {
		fmt.Printf("Sending request to %s\n", url)
	}

	resp, err := client.Do(req)
	if err != nil {
		result.Error = err
		result.Duration = time.Since(startTime)
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	result.Duration = time.Since(startTime)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Error = fmt.Errorf("error reading response body: %s", err)
		return result
	}

	result.ResponseBody = string(body)
	result.Success = isStatusAcceptable(resp.StatusCode, target.StatusCodes, statusRanges)

	return result
}

// constructURL builds the full URL from base URL and endpoint
func constructURL(baseURL, endpoint string) string {
	if endpoint == "" {
		return baseURL
	}
	if endpoint[0] != '/' && baseURL[len(baseURL)-1] != '/' {
		return baseURL + "/" + endpoint
	}
	return baseURL + endpoint
}

// printResults formats and prints the collected endpoint results
func printResults(results []EndpointResult, green, red func(a ...interface{}) string, verbose bool) {
	var successful, failed int
	var totalDuration time.Duration

	for _, result := range results {
		if result.Error != nil {
			fmt.Println(red(fmt.Sprintf("GET %s - Error: %v", result.URL, result.Error)))
			failed++
			continue
		}

		if result.Success {
			responseMsg := fmt.Sprintf("GET %s - %d (%.2fs)", result.URL, result.StatusCode, result.Duration.Seconds())
			if verbose {
				responseMsg += fmt.Sprintf(" - %s", result.ResponseBody)
			}
			fmt.Println(green(responseMsg))
			successful++
		} else {
			fmt.Println(red(fmt.Sprintf("GET %s - %d - %s", result.URL, result.StatusCode, result.ResponseBody)))
			failed++
		}

		totalDuration += result.Duration
	}

	// Print statistics summary
	total := successful + failed
	if total > 0 {
		avgDuration := totalDuration / time.Duration(total)
		fmt.Println("\nSummary:")
		fmt.Printf("Total: %d, Successful: %d, Failed: %d\n", total, successful, failed)
		fmt.Printf("Average response time: %.2fs\n", avgDuration.Seconds())
	}
}

func main() {
	flags := parseFlags()

	config, err := loadConfig(flags.configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}

	client := setupHTTPClient(config.Global.Timeout, flags.timeout)
	green, red := setupColorOutput()

	// Create a semaphore if concurrency is limited
	var sem chan struct{}
	if flags.concurrency > 0 {
		sem = make(chan struct{}, flags.concurrency)
	}

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
		results := processTarget(client, target, statusRanges, &wg, sem, flags.verbosity)
		wg.Wait()

		// Print the results after all checks are complete
		printResults(results, green, red, flags.verbosity)
	}
}
