package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"strconv"
	"net/textproto"
	"path/filepath"
)

// Ensures gofmt doesn't remove the "net" and "os" imports above (feel free to remove this!)
var _ = net.Listen
var _ = os.Exit

func parseRequestLine(firstLine string) (method, path, version string, err error) {
	parts := strings.Split(firstLine, " ")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid request line: %s", firstLine)
	}
	return parts[0], parts[1], parts[2], nil
}

func parseHeaders(reqLines []string) (headers map[string]string, bodyStartIndex int) {
	headers = make(map[string]string)
	bodyStartIndex = -1
	for i, line := range reqLines {
		if line == "" {
			bodyStartIndex = i + 1
			break // End of headers
		}
		parts := strings.SplitN(line, ":", 2) // Split at the first colon
		if len(parts) == 2 {
			headerName := strings.TrimSpace(parts[0])
			headerValue := strings.TrimSpace(parts[1])
			canonicalName := textproto.CanonicalMIMEHeaderKey(headerName)
			headers[canonicalName] = headerValue
		}
	}
	// If no empty line is found, it means there's no body or it's a simple request
	if bodyStartIndex == -1 {
		bodyStartIndex = len(reqLines)
	}
	return headers, bodyStartIndex
}

func handleRootRequest(conn net.Conn, headers map[string]string) {
	res := "HTTP/1.1 200 OK\r\n\r\n"
	connectionVal, hasConnection := headers["Connection"]
	closeConnection := hasConnection && strings.Contains(strings.ToLower(connectionVal), "close")

	if closeConnection {
		res = strings.Replace(res, "\r\n\r\n", "\r\nConnection: close\r\n\r\n", 1)
	}
	conn.Write([]byte(res))
	if closeConnection {
		conn.Close()
	}
}

func handleEchoRequest(conn net.Conn, headers map[string]string, path string) {
	pathStr := strings.Split(path, "/echo/")[1]
	res := ""
	connectionVal, hasConnection := headers["Connection"]
	closeConnection := hasConnection && strings.Contains(strings.ToLower(connectionVal), "close")

	if contentEncoding, ok := headers["Accept-Encoding"]; ok {
		encodings := strings.Split(contentEncoding, ", ")
		for _, enc := range encodings {
			if enc == "gzip" {
				compressedData, err := compressData(pathStr)
				if err != nil {
					fmt.Printf("Error compressing data for /echo: %v\n", err)
					errorRes := "HTTP/1.1 500 Internal Server Error\r\n"
					if closeConnection {
						errorRes += "Connection: close\r\n"
					}
					errorRes += "\r\n" // Ensure empty line for end of headers
					conn.Write([]byte(errorRes))
					if closeConnection {
						conn.Close()
					}
					return
				}
				resHeaders := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Encoding: gzip\r\nContent-Length: %d\r\n", compressedData.Len())
				if closeConnection {
					resHeaders += "Connection: close\r\n"
				}
				resHeaders += "\r\n"
				
				conn.Write([]byte(resHeaders))
				conn.Write(compressedData.Bytes())
				if closeConnection {
					conn.Close()
				}
				return
			}
		}
	}

	res = fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n", len(pathStr))
	if closeConnection {
		res += "Connection: close\r\n"
	}
	res += fmt.Sprintf("\r\n%s", pathStr)
	
	conn.Write([]byte(res))
	if closeConnection {
		conn.Close()
	}
}

func handleUserAgentRequest(conn net.Conn, headers map[string]string) {
	userAgent := headers["User-Agent"]
	res := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n", len(userAgent))
	connectionVal, hasConnection := headers["Connection"]
	closeConnection := hasConnection && strings.Contains(strings.ToLower(connectionVal), "close")

	if closeConnection {
		res += "Connection: close\r\n"
	}
	res += fmt.Sprintf("\r\n%s", userAgent)

	conn.Write([]byte(res))
	if closeConnection {
		conn.Close()
	}
}

func handleFileRequest(conn net.Conn, headers map[string]string, path string, method string, dir string, reqLines []string) {
	requestedFileName := strings.Split(path, "/files/")[1]

	// Get absolute base directory path
	absBaseDir, err := filepath.Abs(dir)
	if err != nil {
		fmt.Printf("Error getting absolute path for base directory %s: %v\n", dir, err)
		// This is a server configuration error, send 500
		res := "HTTP/1.1 500 Internal Server Error\r\n\r\n"
		conn.Write([]byte(res))
		return
	}

	// Construct the full path and clean it
	unsafeFilePath := filepath.Join(absBaseDir, requestedFileName)
	cleanedFilePath := filepath.Clean(unsafeFilePath)


	// Security Check: Ensure the cleaned path is still within the base directory
	if !strings.HasPrefix(cleanedFilePath, absBaseDir) {
		fmt.Printf("Path traversal attempt detected: original '%s', cleaned '%s', base '%s'\n", requestedFileName, cleanedFilePath, absBaseDir)
		res := "HTTP/1.1 403 Forbidden\r\n\r\n"
		conn.Write([]byte(res))
		// Note: Connection closing for 403 is not explicitly handled here but will be by defer in handleRequest
		// Or, if specific Connection: close header is present, it will be handled by defer.
		// For simplicity, we send the response and return.
		return
	}
	
	fmt.Printf("Accessing File Path: %s\n", cleanedFilePath) // Use cleanedFilePath for operations
	var res string 

	connectionVal, hasConnection := headers["Connection"]
	closeConnection := hasConnection && strings.Contains(strings.ToLower(connectionVal), "close")

	if method == "GET" {
		fileContent, err := os.ReadFile(cleanedFilePath) // Use cleanedFilePath
		if err != nil {
			fmt.Printf("Error reading file %s: %v\n", cleanedFilePath, err)
			res = "HTTP/1.1 404 Not Found\r\n" 
		} else {
			resHeaders := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\n", len(fileContent))
			if closeConnection {
				resHeaders += "Connection: close\r\n"
			}
			resHeaders += "\r\n" 

			conn.Write([]byte(resHeaders))
			conn.Write(fileContent) 
			if closeConnection {
				conn.Close()
			}
			return 
		}
	} else if method == "POST" {
		var postData string
		if len(reqLines) > 0 {
			bodyStartIndex := -1
			for i, line := range reqLines {
				if line == "" {
					bodyStartIndex = i + 1
					break
				}
			}
			if bodyStartIndex != -1 && bodyStartIndex < len(reqLines) {
				fullBody := strings.Join(reqLines[bodyStartIndex:], "\r\n")
				postData = strings.TrimRight(fullBody, "\x00")
			}
		}
		fmt.Printf("Post Data to write: [%s] to file %s\n", postData, cleanedFilePath)
		err := os.WriteFile(cleanedFilePath, []byte(postData), 0644) // Use cleanedFilePath
		if err != nil {
			fmt.Printf("Error writing file %s: %v\n", cleanedFilePath, err)
			res = "HTTP/1.1 500 Internal Server Error\r\n"
		} else {
			res = "HTTP/1.1 201 Created\r\n"
		}
	} else {
		res = "HTTP/1.1 405 Method Not Allowed\r\n"
	}

	var finalResBuilder strings.Builder
	finalResBuilder.WriteString(strings.TrimSuffix(res, "\r\n")) 
	finalResBuilder.WriteString("\r\n") 

	if closeConnection {
		finalResBuilder.WriteString("Connection: close\r\n")
	}
	finalResBuilder.WriteString("\r\n") 
	
	conn.Write([]byte(finalResBuilder.String()))
	if closeConnection {
		conn.Close()
	}
}

func compressData(data string) (bytes.Buffer, error) { // Corrected signature from previous step
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, err := writer.Write([]byte(data))
	if err != nil {
		return buf, fmt.Errorf("failed to write data to gzip writer: %w", err)
	}
	err = writer.Close() // Ensure writer is closed to flush all data to buf
	if err != nil {
		return buf, fmt.Errorf("failed to close gzip writer: %w", err)
	}
	// fmt.Println("Buffer: ", buf) // Kept for debugging if necessary
	return buf, nil
}

func main() {
	fmt.Println("Logs from your program will appear here!")

	dir := ""
	if len(os.Args) == 3 {
		dir = os.Args[2]
	}

	fmt.Printf("Using dir: %s\n", dir)
	l, err := net.Listen("tcp", "0.0.0.0:4221")
	if err != nil {
		fmt.Printf("Failed to bind to port 4221: %v\n", err) // Log specific error
		os.Exit(1) // Exit if listener can't be set up.
	}
	defer l.Close() // Ensure listener is closed when main exits.

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Printf("Error accepting connection: %v\n", err.Error())
			// Instead of exiting, continue to the next iteration 
			// to allow the server to keep running for other connections.
			continue
		}
		go handleRequest(conn, dir)
	}
}

func handleRequest(conn net.Conn, dir string) {
	defer conn.Close()
	buf := make([]byte, 1024)

	n, err := conn.Read(buf)
	if err != nil {
		if err != io.EOF {
			fmt.Println("Error reading request:", err)
		}
		return
	}

	requestString := string(buf[:n])
	reqLines := strings.Split(requestString, "\r\n")

	if len(reqLines) == 0 {
		fmt.Println("Empty request received")
		return
	}

	method, path, _, err := parseRequestLine(reqLines[0])
	if err != nil {
		fmt.Println("Error parsing request line:", err)
		// Send a 400 Bad Request response
		conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	headers, _ := parseHeaders(reqLines[1:]) // Pass lines after the request line

	// Debugging output (optional)
	fmt.Printf("Method: %s, Path: %s\n", method, path)
	fmt.Printf("Headers: %v\n", headers)

	switch {
	case path == "/":
		handleRootRequest(conn, headers)
	case strings.HasPrefix(path, "/echo/"):
		handleEchoRequest(conn, headers, path)
	case path == "/user-agent":
		handleUserAgentRequest(conn, headers)
	case strings.HasPrefix(path, "/files/"):
		handleFileRequest(conn, headers, path, method, dir, reqLines)
	default:
		conn.Write([]byte("HTTP/1.1 404 Not Found\r\n\r\n"))
	}
}
