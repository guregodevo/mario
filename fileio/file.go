package fileio

// WalkFunc is the callback function used in the Walk method.
type WalkFunc func(path string) error

// FileIO defines a generic interface for file operations.
type FileIO interface {
	Read(path string) ([]byte, error)                        // Reads a file and returns its content.
	Write(path string, content []byte) error                 // Writes content to a file (creates or updates).
	Delete(path string) error                                // Deletes the file.
	Walk(root string, walkFn WalkFunc) error                 // Walks through files in a directory or bucket.
	ExtractInfo(path string) (string, string, string, error) // New method for extracting info
	MkdirAll(string) error                                   // Ensure the necessary directory structure exists
}
