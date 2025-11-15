package server

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/dundee/gdu/v5/internal/testdir"
	"github.com/stretchr/testify/assert"
)

// TestUnixSocketServerEndToEnd tests complete end-to-end flow with real Unix socket
func TestUnixSocketServerEndToEnd(t *testing.T) {
	// Create unique socket path
	socketPath := "/tmp/test-gdu-e2e-" + time.Now().Format("20060102150405") + ".sock"
	defer os.Remove(socketPath)

	// Create test directory
	fin := testdir.CreateTestDir()
	defer fin()

	// Create and start server
	server, err := NewUnixSocketServer(socketPath, false, "")
	assert.NoError(t, err)

	go func() {
		err := server.Start()
		assert.NoError(t, err)
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Connect client
	conn, err := net.Dial("unix", socketPath)
	assert.NoError(t, err)
	defer conn.Close()

	// Test 1: progress before scan
	progressReq := Request{
		ID:     "progress-1",
		Method: "progress",
		Params: map[string]interface{}{},
	}
	err = sendSocketRequest(conn, progressReq)
	assert.NoError(t, err)

	resp, err := readSocketResponse(conn)
	assert.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, "progress-1", resp.ID)

	progressData, ok := resp.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.False(t, progressData["is_scanning"].(bool))

	// Test 2: start scan
	scanReq := Request{
		ID:     "scan-1",
		Method: "scan",
		Params: map[string]interface{}{"path": "test_dir"},
	}
	err = sendSocketRequest(conn, scanReq)
	assert.NoError(t, err)

	resp, err = readSocketResponse(conn)
	assert.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, "scan-1", resp.ID)

	startData, ok := resp.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.True(t, startData["started"].(bool))

	// Test 3: query progress during scan
	time.Sleep(100 * time.Millisecond)

	progressReq2 := Request{
		ID:     "progress-2",
		Method: "progress",
		Params: map[string]interface{}{},
	}
	err = sendSocketRequest(conn, progressReq2)
	assert.NoError(t, err)

	resp, err = readSocketResponse(conn)
	assert.NoError(t, err)
	assert.True(t, resp.Success)

	// Should be scanning now
	progressData2, ok := resp.Data.(map[string]interface{})
	assert.True(t, ok)
	// Note: We might catch it before it starts or after it finishes
	_ = progressData2["is_scanning"].(bool)

	// Test 4: query directory (root)
	// Wait for scan to complete
	time.Sleep(2 * time.Second)

	dirReq := Request{
		ID:     "dir-1",
		Method: "directory",
		Params: map[string]interface{}{"path": "", "depth": 1},
	}
	err = sendSocketRequest(conn, dirReq)
	assert.NoError(t, err)

	resp, err = readSocketResponse(conn)
	assert.NoError(t, err)
	assert.True(t, resp.Success)

	dirData, ok := resp.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.NotEmpty(t, dirData["name"])
	assert.True(t, dirData["is_dir"].(bool))
	assert.Greater(t, int(dirData["item_count"].(float64)), 0)

	// Test 5: cancel (even though scan is done, should handle gracefully)
	cancelReq := Request{
		ID:     "cancel-1",
		Method: "cancel",
		Params: map[string]interface{}{},
	}
	err = sendSocketRequest(conn, cancelReq)
	assert.NoError(t, err)

	resp, err = readSocketResponse(conn)
	assert.NoError(t, err)
	assert.True(t, resp.Success)

	cancelData, ok := resp.Data.(map[string]interface{})
	assert.True(t, ok)
	assert.True(t, cancelData["cancelled"].(bool))
}


// TestSocketErrorHandling tests error handling over socket
func TestSocketErrorHandling(t *testing.T) {
	socketPath := "/tmp/test-gdu-err-" + time.Now().Format("20060102150405") + ".sock"
	defer os.Remove(socketPath)

	server, err := NewUnixSocketServer(socketPath, false, "")
	assert.NoError(t, err)

	go server.Start()
	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("unix", socketPath)
	assert.NoError(t, err)
	defer conn.Close()

	// Test 1: Invalid method
	invalidReq := Request{
		ID:     "invalid-1",
		Method: "invalid_method",
		Params: map[string]interface{}{},
	}
	err = sendSocketRequest(conn, invalidReq)
	assert.NoError(t, err)

	resp, err := readSocketResponse(conn)
	assert.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "Unknown method")

	// Test 2: Missing parameter
	scanReq := Request{
		ID:     "scan-no-path",
		Method: "scan",
		Params: map[string]interface{}{}, // missing 'path'
	}
	err = sendSocketRequest(conn, scanReq)
	assert.NoError(t, err)

	resp, err = readSocketResponse(conn)
	assert.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "missing parameter")

	// Test 3: Invalid parameter type
	scanReq2 := Request{
		ID:     "scan-wrong-type",
		Method: "scan",
		Params: map[string]interface{}{
			"path": 123, // should be string
		},
	}
	err = sendSocketRequest(conn, scanReq2)
	assert.NoError(t, err)

	resp, err = readSocketResponse(conn)
	assert.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "must be string")
}

// TestSocketMultipleSequentialRequests tests multiple sequential requests on same connection
func TestSocketMultipleSequentialRequests(t *testing.T) {
	socketPath := "/tmp/test-gdu-seq-" + time.Now().Format("20060102150405") + ".sock"
	defer os.Remove(socketPath)

	server, err := NewUnixSocketServer(socketPath, false, "")
	assert.NoError(t, err)

	go server.Start()
	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("unix", socketPath)
	assert.NoError(t, err)
	defer conn.Close()

	// Send multiple progress requests in sequence
	for i := 0; i < 5; i++ {
		req := Request{
			ID:     "progress-seq-" + string(rune('0'+i)),
			Method: "progress",
			Params: map[string]interface{}{},
		}

		err = sendSocketRequest(conn, req)
		assert.NoError(t, err)

		resp, err := readSocketResponse(conn)
		assert.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, req.ID, resp.ID)
	}
}

// TestSocketConnectionClose tests graceful connection close
func TestSocketConnectionClose(t *testing.T) {
	socketPath := "/tmp/test-gdu-close-" + time.Now().Format("20060102150405") + ".sock"
	defer os.Remove(socketPath)

	server, err := NewUnixSocketServer(socketPath, false, "")
	assert.NoError(t, err)

	go server.Start()
	time.Sleep(100 * time.Millisecond)

	// Connect and immediately close
	conn, err := net.Dial("unix", socketPath)
	assert.NoError(t, err)
	conn.Close()

	// Server should still be running
	time.Sleep(50 * time.Millisecond)

	// New connection should work
	conn2, err := net.Dial("unix", socketPath)
	assert.NoError(t, err)
	defer conn2.Close()

	req := Request{
		ID:     "after-close",
		Method: "progress",
		Params: map[string]interface{}{},
	}
	err = sendSocketRequest(conn2, req)
	assert.NoError(t, err)
}

// Helper functions for socket communication

func sendSocketRequest(conn net.Conn, req Request) error {
	data, err := json.Marshal(req)
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

func readSocketResponse(conn net.Conn) (*Response, error) {
	// Read length
	lengthBytes := make([]byte, 4)
	_, err := io.ReadFull(conn, lengthBytes)
	if err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(lengthBytes)

	// Read data
	data := make([]byte, length)
	_, err = io.ReadFull(conn, data)
	if err != nil {
		return nil, err
	}

	// Read newline
	newline := make([]byte, 1)
	_, err = conn.Read(newline)
	if err != nil {
		return nil, err
	}

	// Parse response
	var resp Response
	err = json.Unmarshal(data, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}
