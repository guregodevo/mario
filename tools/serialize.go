package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/guregodevo/mario/fileio"
	"gopkg.in/yaml.v2"
)

type YamlConfigDefinition struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description" yaml:"description"`
	Type        string            `json:"type" yaml:"type"`
	Params      map[string]string `json:"params" yaml:"params"`
	Path        string            `json:"url" yaml:"url"`
	TestInput   string            `json:"test_input" yaml:"test_input"`
	LogoUrl     string            `json:"logoUrl" yaml:"logoUrl"`
}

type YamlConfigMap map[string]map[string]string // {Name:{key:value}}

// ReadYamlToolDefinition reads all YAML files in the given directory and returns a map of YamlToolDefinition.
func ReadYamlConfigDefinition(ioFile fileio.FileIO, path string, names []string) (map[string]*YamlConfigDefinition, error) {
	toolDefinitions := make(map[string]*YamlConfigDefinition)

	// Walk through all the files in the directory (assuming they are YAML files)
	err := ioFile.Walk(path, func(filePath string) error {

		// Only process YAML files
		if filepath.Ext(filePath) == ".yaml" || filepath.Ext(filePath) == ".yml" {
			// Read the file content
			fileContent, err := ioFile.Read(filePath)
			if err != nil {
				return fmt.Errorf("error reading file %s: %w", filePath, err)
			}

			// Parse the YAML content
			var tool YamlConfigDefinition
			err = yaml.Unmarshal(fileContent, &tool)
			if err != nil {
				return fmt.Errorf("error parsing YAML from %s: %w", filePath, err)
			}

			// If the tool is in the names list, add it to the map
			for _, name := range names {
				if tool.Name == name {
					toolDefinitions[tool.Name] = &tool
					break
				}
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return toolDefinitions, nil
}

// ReadConfigMap reads the global config map from a JSON file using the provided FileIO interface.
func ReadConfigMap(ioFile fileio.FileIO, path string) (YamlConfigMap, error) {
	var configMap YamlConfigMap

	// Read the JSON file content
	fileContent, err := ioFile.Read(path)
	if err != nil {
		return nil, fmt.Errorf("error reading config file %s: %w", path, err)
	}

	// Parse the JSON content into the map
	err = json.Unmarshal(fileContent, &configMap)
	if err != nil {
		return nil, fmt.Errorf("error parsing JSON config: %w", err)
	}

	return configMap, nil
}
