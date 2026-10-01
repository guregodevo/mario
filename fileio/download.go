package fileio

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// downloadFile downloads a file from a remote URL and stores it in the /tmp directory.
func DownloadFile(remoteURL string, useCache bool) (string, error) {
	// Get the file name from the URL
	fileName := filepath.Base(remoteURL)

	// Define the file path in the /tmp directory
	tmpFilePath := filepath.Join("/tmp", fileName)

	// Create the file in the /tmp directory
	out, err := os.Create(tmpFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	// Send an HTTP GET request to the remote file URL
	resp, err := http.Get(remoteURL)
	if err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	// Check if the request was successful
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download file: received status code %d", resp.StatusCode)
	}

	// Copy the file contents to the local file in /tmp
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to copy file content: %w", err)
	}

	// Return the file path to the downloaded file
	return tmpFilePath, nil
}
