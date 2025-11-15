// Package server implements Unix socket server with length-prefixed JSON protocol
package server

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/dundee/gdu/v5/internal/common"
	"github.com/dundee/gdu/v5/pkg/fs"
)

// Request represents a client request
type Request struct {
	ID     string                 `json:"id"`
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params"`
}

// Response represents a server response
type Response struct {
	ID      string      `json:"id"`
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// UnixSocketServer provides Unix socket server with length-prefixed JSON protocol
type UnixSocketServer struct {
	server      *Server
	socketPath  string
	listener    net.Listener
	connections sync.WaitGroup
}

// NewUnixSocketServer creates a new Unix socket server
func NewUnixSocketServer(socketPath string, useStorage bool, storagePath string) (*UnixSocketServer, error) {
	// Remove existing socket file if any
	if _, err := os.Stat(socketPath); err == nil {
		if err := os.Remove(socketPath); err != nil {
			return nil, fmt.Errorf("failed to remove existing socket: %w", err)
		}
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create unix socket: %w", err)
	}

	// Set permissions (allow current user to access)
	if err := os.Chmod(socketPath, 0700); err != nil {
		listener.Close()
		return nil, fmt.Errorf("failed to set socket permissions: %w", err)
	}

	return &UnixSocketServer{
		server:     NewServer(useStorage, storagePath),
		socketPath: socketPath,
		listener:   listener,
	}, nil
}

// Start starts the Unix socket server
func (s *UnixSocketServer) Start() error {
	log.Printf("Starting Unix socket server on %s", s.socketPath)
	log.Printf("Protocol: Length-prefixed JSON (4-byte length + JSON + newline)")
	log.Println("")
	log.Println("API Methods:")
	log.Println("  scan       - Start scanning a path")
	log.Println("  progress   - Get current scanning progress")
	log.Println("  cancel     - Cancel current scan")
	log.Println("  directory  - Get directory information")
	log.Println("")
	log.Println("Example request: {\"id\":\"1\",\"method\":\"progress\",\"params\":{}}")
	log.Println("")

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "closed") {
				return nil
			}
			log.Printf("Accept error: %v", err)
			continue
		}

		s.connections.Add(1)
		go s.handleConnection(conn)
	}
}

// Stop stops the Unix socket server
func (s *UnixSocketServer) Stop() error {
	log.Println("Shutting down Unix socket server...")

	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			return err
		}
	}

	// Wait for all connections to finish
	s.connections.Wait()

	// Remove socket file
	if err := os.Remove(s.socketPath); err != nil {
		log.Printf("Warning: failed to remove socket file: %v", err)
	}

	log.Println("Server stopped")
	return nil
}

// handleConnection handles a single client connection
func (s *UnixSocketServer) handleConnection(conn net.Conn) {
	defer s.connections.Done()
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	log.Printf("New connection from %s", remoteAddr)

	reader := bufio.NewReader(conn)

	for {
		// Read length prefix (4 bytes, big-endian)
		lengthBytes := make([]byte, 4)
		if _, err := io.ReadFull(reader, lengthBytes); err != nil {
			if err != io.EOF {
				log.Printf("Error reading length: %v", err)
			}
			return
		}

		length := binary.BigEndian.Uint32(lengthBytes)
		if length == 0 || length > 100*1024*1024 { // Max 100MB
			log.Printf("Invalid message length: %d", length)
			continue
		}

		// Read JSON data
		data := make([]byte, length)
		if _, err := io.ReadFull(reader, data); err != nil {
			log.Printf("Error reading data: %v", err)
			return
		}

		// Read and verify newline
		newline, err := reader.ReadByte()
		if err != nil || newline != '\n' {
			log.Printf("Invalid newline: %v", err)
			return
		}

		// Process request
		response := s.processRequest(data)

		// Send response
		if err := s.sendResponse(conn, response); err != nil {
			log.Printf("Error sending response: %v", err)
			return
		}
	}
}

// processRequest processes a request and returns a response
func (s *UnixSocketServer) processRequest(data []byte) *Response {
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return &Response{
			ID:      "",
			Success: false,
			Error:   fmt.Sprintf("Invalid JSON: %v", err),
		}
	}

	log.Printf("[%s] %s", req.ID, req.Method)

	resp := &Response{
		ID:      req.ID,
		Success: true,
	}

	switch req.Method {
	case "scan":
		path, err := getStringParam(req.Params, "path")
		if err != nil {
			resp.Success = false
			resp.Error = err.Error()
		} else {
			go s.server.scan(path)
			resp.Data = map[string]bool{"started": true}
		}

	case "progress":
		s.server.mu.RLock()
		isScanning := s.server.isScanning
		progress := s.server.progress
		s.server.mu.RUnlock()

		resp.Data = ProgressResponse{
			IsScanning:      isScanning,
			CurrentItemName: progress.CurrentItemName,
			ItemCount:       progress.ItemCount,
			TotalSize:       progress.TotalSize,
		}

	case "cancel":
		s.server.mu.Lock()
		if s.server.cancelFunc != nil {
			s.server.cancelFunc()
			s.server.analyzer.Cancel()
			s.server.cancelFunc = nil
		}
		s.server.isScanning = false
		s.server.progress = common.CurrentProgress{}  // Clear progress state
		s.server.currentDir = nil                     // Clear scan results
		s.server.mu.Unlock()

		resp.Data = map[string]bool{"cancelled": true}

	case "directory":
		path, _ := getStringParam(req.Params, "path")
		depth, _ := getIntParam(req.Params, "depth", 0)

		s.server.mu.RLock()
		if s.server.currentDir == nil {
			s.server.mu.RUnlock()
			resp.Success = false
			resp.Error = "No scan completed"
			break
		}

		var dir fs.Item
		if path == "" {
			dir = s.server.currentDir
		} else {
			dir = findDirectory(s.server.currentDir, path)
		}
		s.server.mu.RUnlock()

		if dir == nil {
			resp.Success = false
			resp.Error = "Directory not found"
		} else {
			resp.Data = convertToDirInfo(dir, depth)
		}

	default:
		resp.Success = false
		resp.Error = fmt.Sprintf("Unknown method: %s", req.Method)
	}

	return resp
}

// sendResponse sends a response to the client
func (s *UnixSocketServer) sendResponse(conn net.Conn, resp *Response) error {
	// Marshal response to JSON
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	// Send length prefix (4 bytes, big-endian)
	length := uint32(len(data))
	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, length)

	if err := writeAll(conn, lengthBytes); err != nil {
		return err
	}

	// Send JSON data
	if err := writeAll(conn, data); err != nil {
		return err
	}

	// Send newline
	return writeAll(conn, []byte{'\n'})
}

// writeAll writes all data to the connection, handling short writes
func writeAll(conn net.Conn, data []byte) error {
	total := 0
	for total < len(data) {
		n, err := conn.Write(data[total:])
		total += n
		if err != nil {
			return err
		}
	}
	return nil
}

// getStringParam gets a string parameter from params map
func getStringParam(params map[string]interface{}, key string) (string, error) {
	if params == nil {
		return "", fmt.Errorf("missing parameter: %s", key)
	}

	val, ok := params[key]
	if !ok {
		return "", fmt.Errorf("missing parameter: %s", key)
	}

	str, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be string", key)
	}

	return str, nil
}

// getIntParam gets an integer parameter from params map
func getIntParam(params map[string]interface{}, key string, defaultValue int) (int, error) {
	if params == nil {
		return defaultValue, nil
	}

	val, ok := params[key]
	if !ok {
		return defaultValue, nil
	}

	// Try as float64 (JSON numbers)
	if f, ok := val.(float64); ok {
		return int(f), nil
	}

	// Try as int
	if i, ok := val.(int); ok {
		return i, nil
	}

	return defaultValue, fmt.Errorf("parameter %s must be integer", key)
}
