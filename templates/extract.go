package templates

import (
	"fmt"
	"github.com/guregodevo/mario/fileio"
	"github.com/guregodevo/mario/logger"
)

type FileProcessor func(string, string, string, string, []byte) (*YamlTaskDefinition, error)

// ExtractYAML Extract YAML Task Definition
func ExtractYAML(ioFileIO fileio.FileIO, validator *YAMLValidator, path string) (*YamlTaskDefinition, error) {
	logger.Log.Info(fmt.Sprintf(" Processing file  %s", path), "component", "yaml")

	// Read YAML file
	yamlContent, r := ioFileIO.Read(path)
	if r != nil {
		return nil, fmt.Errorf("error reading YAML: %v", r)
	}
	logger.Log.Debug(fmt.Sprintf(" Read file  '%s'", path), "component", "yaml")

	errYaml, taskDef := YamlParse(validator, yamlContent)
	if errYaml != nil {
		return nil, errYaml
	}
	logger.Log.Debug(fmt.Sprintf(" Parsed file  '%s'", path), "component", "yaml")

	return taskDef, nil
}
