# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common Development Commands

### Building
```bash
make build              # Build standard binary
make build-static       # Build static binary (no dependencies)
make build-all          # Build for all platforms using gox
make build-docker       # Build Docker image
```

### Testing
```bash
make test               # Run all tests using gotestsum
make coverage           # Run tests with race detection and coverage
make coverage-html      # Generate HTML coverage report
make gobench           # Run Go benchmarks
make benchmark         # Run performance benchmarks with hyperfine
```

### Code Quality
```bash
make lint               # Run golangci-lint with project configuration
```

### Development Workflow
```bash
make run                # Run the application directly
make install-dev-dependencies  # Install development dependencies
make clean              # Clean build artifacts
```

### Running Single Tests
```bash
go test -v ./pkg/analyze/... -run TestSpecificFunction
go test -v ./tui/... -run TestSpecificTUI
```

## High-Level Architecture

### Core Components

1. **File System Abstraction (`pkg/fs/`)**
   - `Item` interface defines common behavior for files/directories
   - `Files` type provides sorting capabilities (size, name, mtime, itemCount)
   - `HardLinkedItems` manages hard link detection

2. **Analysis Engine (`pkg/analyze/`)**
   - `ParallelAnalyzer`: Multi-core parallel scanning optimized for SSD
   - `SequentialAnalyzer`: Single-threaded scanning for HDD
   - Platform-specific implementations (`dir_unix.go`, `dir_linux-openbsd.go`, `dir_other.go`)
   - JSON encoding/decoding for analysis persistence
   - Optional BadgerDB storage for memory optimization

3. **User Interface Layer**
   - `tui/`: Interactive terminal UI using tview library
   - `stdout/`: Non-interactive text output
   - `report/`: JSON export/import functionality

4. **Application Layer (`cmd/gdu/`)**
   - Command-line argument parsing with cobra
   - Configuration file handling (YAML)
   - Mode selection (interactive/non-interactive/export)

### Key Design Patterns

- **Interface-based design**: Heavy use of interfaces (`fs.Item`, `common.ShouldDirBeIgnored`) for flexibility
- **Platform abstraction**: Build tags separate platform-specific code
- **Concurrent processing**: Goroutine pools with controlled concurrency
- **Error handling**: Permission errors are logged but don't stop analysis
- **Memory management**: Automatic GC tuning based on available memory

### Performance Considerations

- Uses `runtime.GOMAXPROCS(0)` for optimal core utilization
- Implements profile-guided optimization (PGO) with `default.pgo`
- Memory usage adapts to system conditions
- Optional constant GC mode for memory-constrained environments

### Cross-Platform Support

- Build tags: `linux`, `darwin`, `windows`, `freebsd`, `netbsd`, `openbsd`, `plan9`
- Architecture support: `amd64`, `arm64`, `arm` (v5, v6, v7)
- Platform-specific device detection in `pkg/device/`
- Different stat implementations for various Unix variants

### Configuration System

- YAML configuration files: `~/.config/gdu/gdu.yaml` or `~/.gdu.yaml`
- Command-line flags override configuration file settings
- Runtime configuration can be saved with `--write-config`