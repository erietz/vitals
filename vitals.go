// Vitals - API Health Check Tool
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"slices"

	"github.com/BurntSushi/toml"
	"github.com/fatih/color"
)

// stringSlice is a custom type that implements flag.Value interface for string slices
type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// Embed the templates directory
//
//go:embed templates/*
var templateFS embed.FS

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
	if slices.Contains(codes, status) {
		return true
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
	configFiles []string
	timeout     int
	verbosity   bool
	concurrency int
	jsonOutput  bool
	htmlOutput  bool
}

// parseFlags parses command line flags
func parseFlags() cliFlags {
	flags := cliFlags{}

	// Define a string slice flag for config files
	flag.Var((*stringSlice)(&flags.configFiles), "config", "Path to configuration file(s)")
	flag.Var((*stringSlice)(&flags.configFiles), "c", "Path to configuration file(s) (shorthand)")

	flag.IntVar(&flags.timeout, "timeout", 0, "Override the global timeout in seconds")
	flag.IntVar(&flags.timeout, "t", 0, "Override the global timeout in seconds (shorthand)")

	flag.BoolVar(&flags.verbosity, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&flags.verbosity, "v", false, "Enable verbose logging (shorthand)")

	flag.IntVar(&flags.concurrency, "concurrency", 0, "Maximum number of concurrent requests (0 means unlimited)")

	flag.BoolVar(&flags.jsonOutput, "json", false, "Output results in JSON format instead of table")
	flag.BoolVar(&flags.jsonOutput, "j", false, "Output results in JSON format instead of table (shorthand)")

	flag.BoolVar(&flags.htmlOutput, "html", false, "Output results in HTML format")
	flag.BoolVar(&flags.htmlOutput, "h", false, "Output results in HTML format (shorthand)")

	// Parse the flags
	flag.Parse()

	// If no config files specified, use the default
	if len(flags.configFiles) == 0 {
		flags.configFiles = append(flags.configFiles, "vitals.toml")
	}

	return flags
}

// loadConfig loads and validates a single configuration file
func loadConfig(configFile string) (Config, error) {
	var config Config
	if _, err := toml.DecodeFile(configFile, &config); err != nil {
		return Config{}, fmt.Errorf("error reading config file %s: %s", configFile, err)
	}
	return config, nil
}

// loadConfigFiles loads multiple configuration files but keeps targets separate with their source filenames
func loadConfigFiles(configFiles []string) ([]ConfigWithSource, error) {
	if len(configFiles) == 0 {
		return nil, fmt.Errorf("no config files specified")
	}

	configsWithSource := make([]ConfigWithSource, 0, len(configFiles))

	// Load each config file separately
	for _, configFile := range configFiles {
		config, err := loadConfig(configFile)
		if err != nil {
			return nil, err
		}

		// Store config with its source filename
		configsWithSource = append(configsWithSource, ConfigWithSource{
			Config:   config,
			Filename: configFile,
		})
	}

	return configsWithSource, nil
}

// ConfigWithSource holds a configuration along with the source filename
type ConfigWithSource struct {
	Config   Config
	Filename string
}

// mergeConfigs merges two Config objects, with values from the second one taking precedence
func mergeConfigs(base, override Config) Config {
	result := base

	// If the override has a non-zero timeout, use that
	if override.Global.Timeout > 0 {
		result.Global.Timeout = override.Global.Timeout
	}

	// Merge targets - add new ones and merge existing ones
	if result.Targets == nil {
		result.Targets = make(map[string]TargetConfig)
	}

	for name, target := range override.Targets {
		if existingTarget, exists := result.Targets[name]; exists {
			// Merge existing target
			result.Targets[name] = mergeTargetConfigs(existingTarget, target)
		} else {
			// Add new target
			result.Targets[name] = target
		}
	}

	return result
}

// mergeTargetConfigs merges two TargetConfig objects
func mergeTargetConfigs(base, override TargetConfig) TargetConfig {
	result := base

	// Use override name if specified
	if override.Name != "" {
		result.Name = override.Name
	}

	// Add new base URLs
	for _, baseURL := range override.BaseURLs {
		if !slices.Contains(result.BaseURLs, baseURL) {
			result.BaseURLs = append(result.BaseURLs, baseURL)
		}
	}

	// Add new endpoints
	for _, endpoint := range override.Endpoints {
		if !slices.Contains(result.Endpoints, endpoint) {
			result.Endpoints = append(result.Endpoints, endpoint)
		}
	}

	// Merge headers
	if result.Headers == nil {
		result.Headers = make(map[string]string)
	}
	for key, value := range override.Headers {
		result.Headers[key] = value
	}

	// Add new status codes
	for _, code := range override.StatusCodes {
		if !slices.Contains(result.StatusCodes, code) {
			result.StatusCodes = append(result.StatusCodes, code)
		}
	}

	// Add new status ranges
	for _, rangeStr := range override.StatusRanges {
		if !slices.Contains(result.StatusRanges, rangeStr) {
			result.StatusRanges = append(result.StatusRanges, rangeStr)
		}
	}

	return result
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
func setupColorOutput() (func(a ...interface{}) string, func(a ...interface{}) string, func(a ...interface{}) string) {
	return color.New(color.FgGreen).SprintFunc(),
		color.New(color.FgRed).SprintFunc(),
		color.New(color.Reset).SprintFunc() // Add neutral color for borders
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
func processTarget(client *http.Client, target TargetConfig, statusRanges []StatusRange, sem chan struct{}, verbose bool) []EndpointResult {
	resultsCount := len(target.BaseURLs) * len(target.Endpoints)
	resultsChan := make(chan EndpointResult, resultsCount)

	for _, baseURL := range target.BaseURLs {
		for _, endpoint := range target.Endpoints {
			go func(baseURL, endpoint string) {
				// If semaphore is provided, use it to limit concurrency
				if sem != nil {
					sem <- struct{}{}        // Acquire
					defer func() { <-sem }() // Release
				}

				resultsChan <- checkEndpoint(client, baseURL, endpoint, target, statusRanges, verbose)
			}(baseURL, endpoint)
		}
	}

	// Collect results from the channel until it's closed
	results := make([]EndpointResult, 0, resultsCount)

	for range resultsCount {
		results = append(results, <-resultsChan)
	}

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

// printDivider prints a horizontal divider line for the table
func printDivider(widths map[string]int, neutral func(a ...interface{}) string) {
	divider := "+"
	for _, width := range []string{"METHOD", "URL", "STATUS", "DURATION", "RESULT"} {
		divider += strings.Repeat("-", widths[width]+2) + "+"
	}
	fmt.Println(neutral(divider))
}

// printRow prints a single row of the table with proper padding
func printRow(method, url string, status interface{}, duration, result string, widths map[string]int, rowColor, neutral func(a ...interface{}) string) {
	// Split the row into parts for proper coloring
	parts := []string{
		fmt.Sprintf(" %-*s ", widths["METHOD"], method),
		fmt.Sprintf(" %-*s ", widths["URL"], url),
		fmt.Sprintf(" %-*v ", widths["STATUS"], status),
		fmt.Sprintf(" %-*s ", widths["DURATION"], duration),
		fmt.Sprintf(" %-*s ", widths["RESULT"], result),
	}

	// Build colored row with neutral borders
	var coloredRow string
	coloredRow += neutral("|")

	for i, part := range parts {
		coloredRow += rowColor(part) + neutral("|")
		if i < len(parts)-1 {
			// No need to add extra pipe after last part
		}
	}

	fmt.Println(coloredRow)
}

// printResults formats and prints the collected endpoint results in a table
func printResults(results []EndpointResult, targetName string, configName string, green, red func(a ...interface{}) string, verbose bool) {
	var successful, failed int
	var totalDuration time.Duration

	// Get neutral color for borders
	_, _, neutral := setupColorOutput()

	// Calculate column widths
	widths := map[string]int{
		"METHOD":   6,  // "METHOD"
		"URL":      3,  // "URL"
		"STATUS":   6,  // "STATUS"
		"DURATION": 10, // "DURATION"
		"RESULT":   6,  // "RESULT"
	}

	// Pre-process results to determine column widths
	tableData := make([][]string, 0, len(results))
	for _, result := range results {
		method := "GET"
		urlStr := result.URL
		var status interface{}
		duration := fmt.Sprintf("%.2fs", result.Duration.Seconds())
		var resultStr string

		if result.Error != nil {
			status = "ERROR"
			resultStr = fmt.Sprintf("Error: %v", result.Error)
			failed++
		} else {
			status = result.StatusCode
			if result.Success {
				resultStr = "Success"
				successful++
			} else {
				resultStr = "Failed"
				failed++
			}
		}

		// Update max widths
		if len(method) > widths["METHOD"] {
			widths["METHOD"] = len(method)
		}
		if len(urlStr) > widths["URL"] {
			widths["URL"] = len(urlStr)
		}
		// Limit URL length if it's too long
		if widths["URL"] > 60 {
			widths["URL"] = 60
		}
		statusLen := len(fmt.Sprintf("%v", status))
		if statusLen > widths["STATUS"] {
			widths["STATUS"] = statusLen
		}
		if len(duration) > widths["DURATION"] {
			widths["DURATION"] = len(duration)
		}
		if len(resultStr) > widths["RESULT"] {
			widths["RESULT"] = len(resultStr)
		}

		tableData = append(tableData, []string{method, urlStr, fmt.Sprintf("%v", status), duration, resultStr})
		totalDuration += result.Duration
	}

	// Calculate table width for title row
	totalWidth := 1 // Initial "+" character
	for _, width := range []string{"METHOD", "URL", "STATUS", "DURATION", "RESULT"} {
		totalWidth += widths[width] + 3 // width + 2 for padding + 1 for border
	}

	// Construct the title with target and config file names
	title := fmt.Sprintf("[%s] from %s", targetName, configName)

	// Print title row with target name centered first
	titleDivider := "+" + strings.Repeat("-", totalWidth-2) + "+"
	fmt.Println(neutral(titleDivider))
	padding := (totalWidth - 2 - len(title)) / 2
	if padding < 0 {
		padding = 1
	}
	titleRow := "|" + strings.Repeat(" ", padding) + title
	titleRow += strings.Repeat(" ", totalWidth-2-padding-len(title)) + "|"
	fmt.Println(neutral(titleRow))
	fmt.Println(neutral(titleDivider))

	// Print table header AFTER the title row
	printRow("METHOD", "URL", "STATUS", "DURATION", "RESULT", widths, neutral, neutral)
	printDivider(widths, neutral)

	// Print table rows
	for i, row := range tableData {
		method := row[0]
		url := row[1]
		if len(url) > 60 {
			url = url[:57] + "..."
		}
		status := row[2]
		duration := row[3]
		resultStr := row[4]

		if strings.HasPrefix(resultStr, "Error:") || resultStr == "Failed" {
			// Color the row content red for failures, but borders neutral
			printRow(method, url, status, duration, resultStr, widths, red, neutral)
		} else {
			// Color the row content green for successes, but borders neutral
			printRow(method, url, status, duration, resultStr, widths, green, neutral)
		}

		// If verbose and there's response body, print it under the row
		if verbose && len(results[i].ResponseBody) > 0 && results[i].Error == nil {
			responseWidth := widths["METHOD"] + widths["URL"] + widths["STATUS"] + widths["DURATION"] + widths["RESULT"] + 10
			fmt.Print(neutral("| "))
			fmt.Print(strings.Repeat(" ", responseWidth))
			fmt.Println(neutral(" |"))

			// Truncate response body if too long
			responseBody := results[i].ResponseBody
			maxBodyLen := responseWidth - 11 // Account for "Response: "
			if len(responseBody) > maxBodyLen {
				responseBody = responseBody[:maxBodyLen-3] + "..."
			}

			fmt.Print(neutral("| "))
			fmt.Printf("Response: %-*s", maxBodyLen, responseBody)
			fmt.Println(neutral(" |"))
		}
	}

	// Print summary statistics row
	total := successful + failed
	if total > 0 {
		printDivider(widths, neutral)
		avgDuration := totalDuration / time.Duration(total)
		summaryStr := fmt.Sprintf("Total: %d, Success: %d, Failed: %d, Avg: %.2fs",
			total, successful, failed, avgDuration.Seconds())

		// Create a single row for the summary that spans all columns
		fmt.Print(neutral("| "))
		if failed > 0 {
			fmt.Print(red(fmt.Sprintf("%-*s", totalWidth-4, summaryStr)))
		} else {
			fmt.Print(green(fmt.Sprintf("%-*s", totalWidth-4, summaryStr)))
		}
		fmt.Println(neutral(" |"))
	}

	printDivider(widths, neutral)
}

// JSONResult represents a JSON-serializable version of EndpointResult
type JSONResult struct {
	URL          string  `json:"url"`
	Method       string  `json:"method"`
	StatusCode   int     `json:"status_code,omitempty"`
	Duration     float64 `json:"duration_seconds"`
	Success      bool    `json:"success"`
	Error        string  `json:"error,omitempty"`
	ResponseBody string  `json:"response_body,omitempty"`
}

// JSONTargetResults represents results for a single target in JSON format
type JSONTargetResults struct {
	Target     string       `json:"target"`
	ConfigFile string       `json:"config_file"` // Added config file name
	Results    []JSONResult `json:"results"`
	Summary    JSONSummary  `json:"summary"`
}

// JSONSummary contains summary statistics for a target
type JSONSummary struct {
	Total       int     `json:"total"`
	Successful  int     `json:"successful"`
	Failed      int     `json:"failed"`
	AvgDuration float64 `json:"avg_duration_seconds"`
}

// JSONOutput represents the complete JSON output format
type JSONOutput struct {
	Targets map[string]JSONTargetResults `json:"targets"`
}

// HTMLTemplateData represents the data passed to the HTML template
type HTMLTemplateData struct {
	Targets map[string]JSONTargetResults
	Verbose bool
}

// printJSONResults formats and prints the collected endpoint results as JSON
func printJSONResults(results []EndpointResult, targetName string, configName string, verbose bool) (JSONTargetResults, error) {
	var successful, failed int
	var totalDuration time.Duration

	// Convert to JSON-friendly format
	jsonResults := make([]JSONResult, 0, len(results))
	for _, result := range results {
		jsonResult := JSONResult{
			URL:      result.URL,
			Method:   "GET",
			Duration: result.Duration.Seconds(),
			Success:  result.Success,
		}

		if result.Error != nil {
			jsonResult.Error = result.Error.Error()
			failed++
		} else {
			jsonResult.StatusCode = result.StatusCode
			if result.Success {
				successful++
			} else {
				failed++
			}
		}

		// Include response body only in verbose mode
		if verbose && len(result.ResponseBody) > 0 && result.Error == nil {
			jsonResult.ResponseBody = result.ResponseBody
		}

		jsonResults = append(jsonResults, jsonResult)
		totalDuration += result.Duration
	}

	// Create summary
	total := successful + failed
	var avgDuration float64
	if total > 0 {
		avgDuration = totalDuration.Seconds() / float64(total)
	}

	summary := JSONSummary{
		Total:       total,
		Successful:  successful,
		Failed:      failed,
		AvgDuration: avgDuration,
	}

	// Create target results
	targetResults := JSONTargetResults{
		Target:     targetName,
		ConfigFile: configName, // Include config file name
		Results:    jsonResults,
		Summary:    summary,
	}

	return targetResults, nil
}

// generateHTMLResults formats the endpoint results into HTML using the embedded template
func generateHTMLResults(allTargets map[string]JSONTargetResults, verbose bool) (string, error) {
	// Create template data
	data := HTMLTemplateData{
		Targets: allTargets,
		Verbose: verbose,
	}

	// Parse the template from embedded file
	tmpl, err := template.ParseFS(templateFS, "templates/report.html")
	if err != nil {
		return "", fmt.Errorf("error parsing HTML template: %v", err)
	}

	// Render the template to a string
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("error executing HTML template: %v", err)
	}

	return buf.String(), nil
}

func main() {
	flags := parseFlags()
	configs, err := loadConfigFiles(flags.configFiles)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}

	// Only print a newline in table mode
	if !flags.jsonOutput && !flags.htmlOutput {
		fmt.Println()
	}

	// Always collect results for all targets in case of JSON or HTML output
	collectResults := flags.jsonOutput || flags.htmlOutput
	jsonOutput := JSONOutput{Targets: make(map[string]JSONTargetResults)}

	for _, configWithSource := range configs {
		config := configWithSource.Config
		configName := configWithSource.Filename

		// Set up HTTP client with timeout from this config
		client := setupHTTPClient(config.Global.Timeout, flags.timeout)
		green, red, _ := setupColorOutput()

		// Create a semaphore if concurrency is limited
		var sem chan struct{}
		if flags.concurrency > 0 {
			sem = make(chan struct{}, flags.concurrency)
		}

		// Process each target from this config file
		for targetName, target := range config.Targets {
			// Create a unique key for this target in this config file
			uniqueTargetKey := fmt.Sprintf("%s::%s", configName, targetName)

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

			results := processTarget(client, target, statusRanges, sem, flags.verbosity)

			if collectResults {
				jsonTargetResults, err := printJSONResults(results, targetName, configName, flags.verbosity)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error processing results: %s\n", err)
				}
				jsonOutput.Targets[uniqueTargetKey] = jsonTargetResults
			}

			// Only print table output if neither JSON nor HTML is requested
			if !flags.jsonOutput && !flags.htmlOutput {
				printResults(results, targetName, configName, green, red, flags.verbosity)
				fmt.Println()
			}
		}
	}

	// Output the final result in the requested format
	if flags.jsonOutput {
		jsonData, err := json.MarshalIndent(jsonOutput, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling JSON output: %s\n", err)
		} else {
			fmt.Println(string(jsonData))
		}
	} else if flags.htmlOutput {
		htmlOutput, err := generateHTMLResults(jsonOutput.Targets, flags.verbosity)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating HTML output: %s\n", err)
			os.Exit(1)
		}
		fmt.Println(htmlOutput)
	}
}
