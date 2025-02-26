# Vitals

A flexible Go application for monitoring the health and status of API endpoints via HTTP GET requests.

## Features

- Run health checks for multiple API endpoints in parallel
- Configure via TOML configuration file
- Specify HTTP headers for authentication and other requirements
- Define acceptable HTTP status codes and ranges
- Color-coded output for easy readability

## Requirements

- Go 1.16 or higher
- Dependencies:
  - github.com/BurntSushi/toml
  - github.com/fatih/color

## Configuration

Create a TOML configuration file (default: `vitals.toml`) with your API endpoints and settings.

### Configuration Format

```toml
# Global configuration
[global]
timeout = 5  # Maximum request timeout in seconds

# Target configurations
[targets.example]
name = "EXAMPLE API"
base_urls = ["https://api.example.com"]
endpoints = ["health", "status"]
headers = { "Authorization" = "Bearer TOKEN" }
status_codes = [200, 204]
status_ranges = ["200-299"]
```

### Configuration Fields

- `global`: Global settings
  - `timeout`: Maximum time in seconds for each request (default: 5)
  
- `targets`: Map of target configurations
  - `name`: Display name for the target group
  - `base_urls`: List of base URLs to check
  - `endpoints`: List of endpoints to append to each base URL
  - `headers`: Map of HTTP headers to include with each request
  - `status_codes`: List of acceptable HTTP status codes
  - `status_ranges`: List of acceptable HTTP status code ranges (e.g., "200-299")

If no status codes or ranges are specified, only HTTP 200 is accepted.

## Usage

```
./vitals [config_file]
```

If `config_file` is not specified, the application will look for `vitals.toml` in the current directory.

## Example Output


Successful requests will be displayed in green, failed requests in red.
