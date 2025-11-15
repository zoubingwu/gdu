package server

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLengthPrefixedProtocol tests the length-prefixed JSON protocol
func TestLengthPrefixedProtocol(t *testing.T) {
	// Create in-memory pipe (no real files)
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	// Start server handler in background
	go func() {
		// Create a test server
		s := NewServer(false, "")
		handleTestConnection(serverConn, s)
	}()

	// Test request
	req := Request{
		ID:     "test-1",
		Method: "progress",
		Params: map[string]interface{}{},
	}

	// Encode and send
	data, err := json.Marshal(req)
	assert.NoError(t, err)

	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, uint32(len(data)))

	_, err = clientConn.Write(lengthBytes)
	assert.NoError(t, err)

	_, err = clientConn.Write(data)
	assert.NoError(t, err)

	_, err = clientConn.Write([]byte{'\n'})
	assert.NoError(t, err)

	// Read response
	respLengthBytes := make([]byte, 4)
	_, err = io.ReadFull(clientConn, respLengthBytes)
	assert.NoError(t, err)

	respLength := binary.BigEndian.Uint32(respLengthBytes)
	respData := make([]byte, respLength)
	_, err = io.ReadFull(clientConn, respData)
	assert.NoError(t, err)

	// Read newline
	newline := make([]byte, 1)
	_, err = clientConn.Read(newline)
	assert.NoError(t, err)
	assert.Equal(t, byte('\n'), newline[0])

	// Verify response
	var resp Response
	err = json.Unmarshal(respData, &resp)
	assert.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, "test-1", resp.ID)
}

// TestParameterExtraction tests parameter parsing from requests
func TestParameterExtraction(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]interface{}
		key     string
		want    string
		wantErr bool
	}{
		{
			name:    "valid string param",
			params:  map[string]interface{}{"path": "/test/path"},
			key:     "path",
			want:    "/test/path",
			wantErr: false,
		},
		{
			name:    "missing param",
			params:  map[string]interface{}{"other": "value"},
			key:     "path",
			want:    "",
			wantErr: true,
		},
		{
			name:    "nil params",
			params:  nil,
			key:     "path",
			want:    "",
			wantErr: true,
		},
		{
			name:    "wrong type",
			params:  map[string]interface{}{"path": 123},
			key:     "path",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getStringParam(tt.params, tt.key)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// TestIntParameterExtraction tests integer parameter parsing
func TestIntParameterExtraction(t *testing.T) {
	tests := []struct {
		name         string
		params       map[string]interface{}
		key          string
		defaultValue int
		want         int
		wantErr      bool
	}{
		{
			name:         "valid int from float64 (JSON number)",
			params:       map[string]interface{}{"depth": float64(3)},
			key:          "depth",
			defaultValue: 0,
			want:         3,
			wantErr:      false,
		},
		{
			name:         "missing param returns default",
			params:       map[string]interface{}{},
			key:          "depth",
			defaultValue: 5,
			want:         5,
			wantErr:      false,
		},
		{
			name:         "nil params returns default",
			params:       nil,
			key:          "depth",
			defaultValue: 2,
			want:         2,
			wantErr:      false,
		},
		{
			name:         "wrong type",
			params:       map[string]interface{}{"depth": "3"},
			key:          "depth",
			defaultValue: 0,
			want:         0,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getIntParam(tt.params, tt.key, tt.defaultValue)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// TestServerInitialization tests server creation with different configurations
func TestServerInitialization(t *testing.T) {
	t.Run("with storage enabled", func(t *testing.T) {
		server := NewServer(true, "/tmp/test-storage")
		assert.NotNil(t, server)
		assert.NotNil(t, server.analyzer)
	})

	t.Run("with storage disabled", func(t *testing.T) {
		server := NewServer(false, "")
		assert.NotNil(t, server)
		assert.NotNil(t, server.analyzer)
	})

	t.Run("with empty storage path", func(t *testing.T) {
		server := NewServer(true, "")
		assert.NotNil(t, server)
		// Should use default path
	})
}

// handleTestConnection is a simplified connection handler for testing
func handleTestConnection(conn net.Conn, server *Server) {
	defer conn.Close()

	// Read length prefix
	lengthBytes := make([]byte, 4)
	_, err := io.ReadFull(conn, lengthBytes)
	if err != nil {
		return
	}

	length := binary.BigEndian.Uint32(lengthBytes)
	if length == 0 || length > 10*1024*1024 { // Max 10MB for tests
		return
	}

	// Read JSON data
	data := make([]byte, length)
	_, err = io.ReadFull(conn, data)
	if err != nil {
		return
	}

	// Read newline
	newline := make([]byte, 1)
	_, err = conn.Read(newline)
	if err != nil || newline[0] != '\n' {
		return
	}

	// Process request
	var req Request
	err = json.Unmarshal(data, &req)
	if err != nil {
		resp := Response{
			ID:      "",
			Success: false,
			Error:   "Invalid JSON: " + err.Error(),
		}
		sendTestResponse(conn, &resp)
		return
	}

	// Simple response for testing
	resp := Response{
		ID:      req.ID,
		Success: true,
		Data:    map[string]bool{"received": true},
	}
	sendTestResponse(conn, &resp)
}

// sendTestResponse sends a test response
func sendTestResponse(conn net.Conn, resp *Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}

	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, uint32(len(data)))

	if _, err := conn.Write(lengthBytes); err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}
	if _, err := conn.Write([]byte{'\n'}); err != nil {
		return err
	}

	return nil
}
