# Vitals

A Go application for monitoring HTTP endpoint health.

## Features

- Concurrent health checks for HTTP endpoints
- Multiple output formats: CLI table, JSON, HTML report
- Configurable via one or more TOML files
- HTTP header support for authentication
- Custom status code validation (single codes or ranges)
- Concurrency limiting
- Response body inspection in verbose mode
- Color-coded CLI output

## Requirements

- Go 1.16+
- Dependencies are managed via go modules

## Usage

```
vitals [options] [config_file...]
```

### Options

- `-c, --config`: Path to configuration file(s)
- `-t, --timeout`: Override global timeout in seconds
- `-v, --verbose`: Enable verbose logging and response body output
- `--concurrency`: Limit concurrent requests (0 = unlimited)
- `-j, --json`: Output results in JSON format
- `-h, --html`: Output results in HTML format

If no config file is specified, vitals looks for `vitals.toml` in the current directory.

## Configuration

Create TOML configuration files with your API endpoints and settings.

```toml
# Global settings
[global]
timeout = 5  # Request timeout in seconds

# Target configuration
[targets.example]
name = "EXAMPLE API"
base_urls = ["https://api.example.com"]
endpoints = ["health", "status"]
headers = { "Authorization" = "Bearer TOKEN" }
status_codes = [200, 204]
status_ranges = ["200-299"]
```

### Configuration Fields

- `global.timeout`: Default request timeout in seconds
- `targets`: Map of target configurations
  - `name`: Display name
  - `base_urls`: Base URLs to check
  - `endpoints`: Endpoints to append to base URLs
  - `headers`: HTTP headers for requests
  - `status_codes`: Acceptable status codes
  - `status_ranges`: Acceptable status code ranges

If no status codes/ranges specified, only 200 is accepted.
