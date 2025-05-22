package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockConn is a mock implementation of net.Conn for testing.
type mockConn struct {
	readBuffer  bytes.Buffer
	writeBuffer bytes.Buffer
}

func (mc *mockConn) Read(b []byte) (int, error) {
	return mc.readBuffer.Read(b)
}

func (mc *mockConn) Write(b []byte) (int, error) {
	return mc.writeBuffer.Write(b)
}

func (mc *mockConn) Close() error {
	return nil // No-op for testing
}

func (mc *mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4221} // Dummy address
}

func (mc *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345} // Dummy address
}

func (mc *mockConn) SetDeadline(t testing.T) error {
	return nil // No-op
}

func (mc *mockConn) SetReadDeadline(t testing.T) error {
	return nil // No-op
}

func (mc *mockConn) SetWriteDeadline(t testing.T) error {
	return nil // No-op
}

// Helper to prepare a mockConn with specific request data for handlers
func newMockConnWithRequest(requestString string) *mockConn {
	mc := &mockConn{}
	mc.readBuffer.WriteString(requestString)
	return mc
}

func TestParseRequestLine(t *testing.T) {
	// Test case 1: Valid request line
	method, path, version, err := parseRequestLine("GET /index.html HTTP/1.1")
	if err != nil {
		t.Errorf("Expected no error for valid request line, got %v", err)
	}
	if method != "GET" {
		t.Errorf("Expected method GET, got %s", method)
	}
	if path != "/index.html" {
		t.Errorf("Expected path /index.html, got %s", path)
	}
	if version != "HTTP/1.1" {
		t.Errorf("Expected version HTTP/1.1, got %s", version)
	}

	// Test case 2: Invalid request line (missing version)
	_, _, _, err = parseRequestLine("GET /favicon.ico")
	if err == nil {
		t.Errorf("Expected error for invalid request line (missing version), got nil")
	}

	// Test case 3: Invalid request line (too few parts)
	_, _, _, err = parseRequestLine("GET")
	if err == nil {
		t.Errorf("Expected error for invalid request line (too few parts), got nil")
	}

	// Test case 4: Empty request line
	_, _, _, err = parseRequestLine("")
	if err == nil {
		t.Errorf("Expected error for empty request line, got nil")
	}
}

func TestParseHeaders(t *testing.T) {
	rawHeaders := []string{
		"Content-Type: application/json",
		"user-agent: test-client/1.0",
		"X-Custom-Header : value with spaces ",
		"", // Empty line indicating end of headers
		"This is the body, should be ignored by parseHeaders",
	}

	headers, bodyStartIndex := parseHeaders(rawHeaders)

	// Check Content-Type
	expectedContentType := "application/json"
	if contentType, ok := headers["Content-Type"]; !ok || contentType != expectedContentType {
		t.Errorf("Expected Content-Type header '%s', got '%s' (or not found)", expectedContentType, contentType)
	}

	// Check User-Agent (canonicalized)
	expectedUserAgent := "test-client/1.0"
	if userAgent, ok := headers["User-Agent"]; !ok || userAgent != expectedUserAgent {
		t.Errorf("Expected User-Agent header '%s', got '%s' (or not found)", expectedUserAgent, userAgent)
	}
	
	// Check X-Custom-Header (canonicalized and value trimmed)
	expectedCustomHeader := "value with spaces" // Note: textproto.CanonicalMIMEHeaderKey only changes key case. Value trimming is done by our SplitN logic.
	// The current parseHeaders implementation:
	// headerName := strings.TrimSpace(parts[0])
	// headerValue := strings.TrimSpace(parts[1])
	// So, " value with spaces " should become "value with spaces"
	if customHeader, ok := headers["X-Custom-Header"]; !ok || customHeader != expectedCustomHeader {
		t.Errorf("Expected X-Custom-Header header '%s', got '%s' (or not found)", expectedCustomHeader, customHeader)
	}


	// Check bodyStartIndex
	// rawHeaders has 3 header lines, then "", then body. So body starts at index 4 of rawHeaders.
	// parseHeaders receives rawHeaders, so bodyStartIndex should be 3 (index of "" + 1)
	expectedBodyStartIndex := 3 // Index of the line *after* the empty line
	if bodyStartIndex != expectedBodyStartIndex {
		t.Errorf("Expected bodyStartIndex %d, got %d", expectedBodyStartIndex, bodyStartIndex)
	}

	// Test with no body
	rawHeadersNoBody := []string{
		"Host: example.com",
	}
	_, bodyStartIndexNoBody := parseHeaders(rawHeadersNoBody)
	if bodyStartIndexNoBody != len(rawHeadersNoBody) {
		t.Errorf("Expected bodyStartIndex %d for request with no body, got %d", len(rawHeadersNoBody), bodyStartIndexNoBody)
	}
}

func TestCompressData(t *testing.T) {
	originalData := "This is a test string for gzip compression."
	compressedBuffer, err := compressData(originalData)
	if err != nil {
		t.Fatalf("compressData returned an error: %v", err)
	}

	if compressedBuffer.Len() == 0 {
		t.Fatalf("compressData returned an empty buffer.")
	}

	// Decompress the data to verify
	gzipReader, err := gzip.NewReader(&compressedBuffer)
	if err != nil {
		t.Fatalf("Failed to create gzip reader: %v", err)
	}
	defer gzipReader.Close()

	decompressedData, err := io.ReadAll(gzipReader)
	if err != nil {
		t.Fatalf("Failed to read decompressed data: %v", err)
	}

	if string(decompressedData) != originalData {
		t.Errorf("Decompressed data does not match original. Got '%s', want '%s'", string(decompressedData), originalData)
	}
}

func TestHandleFileRequest_PathTraversal(t *testing.T) {
	tempDir := t.TempDir()
	// Create a dummy file inside tempDir to make it a valid base for some scenarios, though not strictly needed for 403 test
	if err := os.WriteFile(filepath.Join(tempDir, "dummy.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create dummy file: %v", err)
	}

	mc := newMockConnWithRequest("") // Request content doesn't matter for this test of handler logic

	// Attempt path traversal
	requestedPath := "/files/../../../../etc/passwd" // Example traversal attempt
	headers := make(map[string]string)
	
	// handleFileRequest(conn net.Conn, headers map[string]string, path string, method string, dir string, reqLines []string)
	handleFileRequest(mc, headers, requestedPath, "GET", tempDir, []string{"GET " + requestedPath + " HTTP/1.1"})

	response := mc.writeBuffer.String()

	if !strings.HasPrefix(response, "HTTP/1.1 403 Forbidden") {
		t.Errorf("Expected 'HTTP/1.1 403 Forbidden' response, got '%s'", response)
	}
}

func TestHandleEchoRequest_Simple(t *testing.T) {
	mc := newMockConnWithRequest("") // Request details don't matter for handler logic test
	
	echoPath := "/echo/hello"
	headers := make(map[string]string) // No special headers for this simple test

	handleEchoRequest(mc, headers, echoPath)

	response := mc.writeBuffer.String()
	
	// Expected response parts
	expectedStatus := "HTTP/1.1 200 OK"
	expectedContentType := "Content-Type: text/plain"
	echoedString := "hello"
	expectedContentLength := fmt.Sprintf("Content-Length: %d", len(echoedString))
	expectedBody := echoedString

	if !strings.HasPrefix(response, expectedStatus) {
		t.Errorf("Expected status '%s', got response prefix '%s'", expectedStatus, response)
	}
	if !strings.Contains(response, expectedContentType) {
		t.Errorf("Expected response to contain '%s', got '%s'", expectedContentType, response)
	}
	if !strings.Contains(response, expectedContentLength) {
		t.Errorf("Expected response to contain '%s', got '%s'", expectedContentLength, response)
	}
	if !strings.HasSuffix(response, "\r\n\r\n"+expectedBody) && !strings.HasSuffix(response, "\n\n"+expectedBody) { // Allow for slight variations in CRLF if any
		// More robustly check the body part after \r\n\r\n
		parts := strings.SplitN(response, "\r\n\r\n", 2)
		if len(parts) < 2 || parts[1] != expectedBody {
			t.Errorf("Expected body '%s', got '%s' from response '%s'", expectedBody, parts[1], response)
		}
	}
}

func TestHandleFileRequest_MethodNotAllowed(t *testing.T) {
	tempDir := t.TempDir()
	// Create a dummy file, its existence doesn't really matter for a 405 test
	// but makes the setup more realistic for a file path.
	dummyFileName := "testfile.txt"
	if err := os.WriteFile(filepath.Join(tempDir, dummyFileName), []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create dummy file: %v", err)
	}

	mc := newMockConnWithRequest("") // Request content doesn't matter

	requestedPath := "/files/" + dummyFileName
	headers := make(map[string]string)
	
	// Call with an unsupported method, e.g., PUT
	handleFileRequest(mc, headers, requestedPath, "PUT", tempDir, []string{"PUT " + requestedPath + " HTTP/1.1"})

	response := mc.writeBuffer.String()

	if !strings.HasPrefix(response, "HTTP/1.1 405 Method Not Allowed") {
		t.Errorf("Expected 'HTTP/1.1 405 Method Not Allowed' response, got '%s'", response)
	}
}
