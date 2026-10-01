package fileio

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/guregodevo/mario/logger"
)

// LocalFileIO implements the FileIO interface for local file system.
type LocalFileIO struct{}

// Read reads a local file and returns its content.
func (l *LocalFileIO) Read(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

// Write writes the content to a local file.
func (l *LocalFileIO) Write(path string, content []byte) error {
	return ioutil.WriteFile(path, content, 0644)
}

// Delete deletes a local file.
func (l *LocalFileIO) Delete(path string) error {
	return os.Remove(path)
}

// Walk traverses the local file system starting from root and applies the walkFn to each file.
func (l *LocalFileIO) Walk(root string, walkFn WalkFunc) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return walkFn(path)
		}
		return nil
	})
}

// ExtractInfo Extract projectID, datasetID, tablename given a YAML file path
func (l *LocalFileIO) ExtractInfo(path string) (string, string, string, error) {
	// Extract project_id and dataset_id from the file path.
	// Assuming the path is like /{PROJECT_ID}/{DATASET_ID}/{TABLE_PATTERN}.yaml
	segments := strings.Split(filepath.Clean(path), string(os.PathSeparator))
	if len(segments) < 3 {
		return "", "", "", fmt.Errorf("invalid path: %s", path)
	}
	projectID := segments[len(segments)-3]
	datasetID := segments[len(segments)-2]
	tableNameSegments := segments[len(segments)-1]
	if len(tableNameSegments) < 2 {
		msg := fmt.Sprintf("Unexpected file format %s", path)
		log.Print(msg)
		return "", "", "", errors.New(msg)
	}
	tablename := strings.Split(segments[len(segments)-1], ".")[0]

	logger.Log.Debug(fmt.Sprintf("project_id:%s dataset_id:%s tablename:%s", projectID, datasetID, tablename), "component", "yaml")

	return projectID, datasetID, tablename, nil
}

// MkdirAll ensures the directory structure exists for the specified path on the local filesystem.
func (l *LocalFileIO) MkdirAll(path string) error {
	err := os.MkdirAll(path, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create directory structure at %s: %v", path, err)
	}
	return nil
}
