package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
)

// Ensures gofmt doesn't remove the "net" and "os" imports above (feel free to remove this!)
var _ = net.Listen
var _ = os.Exit

func comperessData(data string) bytes.Buffer {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, err := writer.Write([]byte(data))
	if err != nil {
		panic(err)
	}
	writer.Close()
	fmt.Println("Buffer: ", buf)
	return buf
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
		fmt.Println("Failed to bind to port 4221")
		os.Exit(1)
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting connection: ", err.Error())
			os.Exit(1)
		}
		go handleRequest(conn, dir)
	}

}

func handleRequest(conn net.Conn, dir string) {
	defer conn.Close()
	buf := make([]byte, 1024)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				fmt.Println("Error reading request:", err)
			}
			return
		}
		req := strings.Split(string(buf), "\r\n")
		userAgent := ""
		connectionClose := false
		requestType := strings.Split(req[0], " ")[0]
		fmt.Println("Request type: ", requestType)

		for i := range req {
			if len(req[i]) > 0 {
				if strings.HasPrefix(req[i], "User-Agent:") {
					userAgent = strings.SplitN(req[i], ": ", 2)[1]
				}
				if strings.HasPrefix(req[i], "Connection:") && strings.Contains(strings.ToLower(req[i]), "close") {
					connectionClose = true
				}
			} else {
				continue
			}
			fmt.Printf("Request received: %s\n", req[i])
		}
		path := strings.Split(req[0], " ")[1]
		var res string

		if path == "/" {
			res = "HTTP/1.1 200 OK\r\n\r\n"
		} else if path[:5] == "/echo" {
			pathStr := strings.Split(path, "/")[len(strings.Split(path, "/"))-1]
			encoding := ""
			for _, line := range req {
				if strings.HasPrefix(line, "Accept-Encoding:") {
					parts := strings.SplitN(line, ": ", 2)
					if len(parts) == 2 {
						encoding = parts[1]
					}
					break
				}
			}
			encoding_list := strings.Split(encoding, ", ")
			for i := range encoding_list {
				if encoding_list[i] == "gzip" {
					encoding = encoding_list[i]
				}
			}
			if encoding != "gzip" {

				res = fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s", len(pathStr), pathStr)
			} else {
				compressedData := comperessData(pathStr)
				headers := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Encoding: gzip\r\nContent-Length: %d\r\n\r\n", compressedData.Len())
				conn.Write([]byte(headers))
				conn.Write(compressedData.Bytes())
				//res = fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Encoding: %s\r\nContent-Length: %d\r\n\r\n%s", encoding, compressedData.Len(), compressedData)
				conn.Close()
				return
			}
		} else if path[:6] == "/files" {
			fileName := strings.Split(path, "/")[len(strings.Split(path, "/"))-1]
			filePath := fmt.Sprintf("%s%s", dir, fileName)
			fmt.Printf("File Path: %s\n", filePath)

			if requestType == "GET" {
				fileContent, err := os.ReadFile(filePath)
				if err != nil {
					res = "HTTP/1.1 404 Not Found\r\n\r\n"
				} else {
					res = fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n%s", len(fileContent), fileContent)

				}
			} else {
				postData := req[len(req)-1]
				postData = strings.TrimRight(postData, "\x00 \n\r\t")
				fmt.Printf("Post Data: %s\n", postData)
				os.WriteFile(filePath, []byte(postData), 0644)
				res = "HTTP/1.1 201 Created\r\n\r\n"
			}
		} else if len(path) < 11 {
			res = "HTTP/1.1 404 Not Found\r\n\r\n"
		} else if path[:11] == "/user-agent" {
			fmt.Printf("Here-Here %s\n", userAgent)
			res = fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s", len(userAgent), userAgent)
		} else {
			res = "HTTP/1.1 404 Not Found\r\n\r\n"
		}
		if connectionClose {
			res = strings.Replace(res, "\r\n\r\n", "\r\nConnection: close\r\n\r\n", 1)
		}

		conn.Write([]byte(res))
		if connectionClose {
			//conn.Write([]byte(res))
			conn.Close()
			return
		}
	}
	conn.Close()
}
