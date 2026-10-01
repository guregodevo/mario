package templates

import (
	"fmt"
	"path/filepath"
	"text/template"

	"github.com/guregodevo/mario/fileio"
	"github.com/guregodevo/mario/logger"
	yaml "gopkg.in/yaml.v2"
)

type FuncMap func(taskDef *YamlTaskDefinition, partition, projectID, datasetID, tableName string) (map[string]interface{}, template.FuncMap, error)

func _outputPath(baseDir, projectID, datasetID, tableName string) string {
	fileName := fmt.Sprintf("%s_%s.yaml", tableName, "YYYYMMDD")

	// Construct the output path with the provided projectID, datasetID, and table name pattern
	return fmt.Sprintf("%s/%s/%s/%s", baseDir, projectID, datasetID, fileName)
}
func DeleteTaskDefinition(ioFileIO fileio.FileIO, baseDir, projectID, datasetID, tableName string) (string, error) {
	outputPath := _outputPath(baseDir, projectID, datasetID, tableName)
	// Write the YAML data to the specified file
	if err := ioFileIO.Delete(outputPath); err != nil {
		return outputPath, fmt.Errorf("error when deleting YAML data from file %s: %v", outputPath, err)
	}
	return outputPath, nil
}

// WriteTaskDefinition writes the YamlTaskDefinition to a YAML file in the specified directory
// with the format: /{projectID}/{datasetID}/{tableName_YYYYMMDD}.yaml
func WriteTaskDefinition(ioFileIO fileio.FileIO, taskDef *YamlTaskDefinition, baseDir, projectID, datasetID, tableName string) (string, error) {
	outputPath := _outputPath(baseDir, projectID, datasetID, tableName)
	// Ensure the necessary directory structure exists
	if err := ioFileIO.MkdirAll(filepath.Dir(outputPath)); err != nil {
		return outputPath, fmt.Errorf("error creating directory for path %s: %v", outputPath, err)
	}

	// Marshal the task definition to YAML
	yamlData, err := yaml.Marshal(taskDef)
	if err != nil {
		return outputPath, fmt.Errorf("error marshaling YAML data: %v", err)
	}

	// Write the YAML data to the specified file
	if err := ioFileIO.Write(outputPath, yamlData); err != nil {
		return outputPath, fmt.Errorf("error writing YAML data to file %s: %v", outputPath, err)
	}

	return outputPath, nil
}

// Walk through the files using the provided FileIO (either local or GCS).
// Walk through the yaml files and templates directory and extract all Task Definitions
func Walk(ioFileIO fileio.FileIO, fn FuncMap, validator *YAMLValidator, taskPath string, partition string, templatePath, fieldName string) (error, map[string]*YamlTaskDefinition) {
	taskDefs := make(map[string]*YamlTaskDefinition, 0)

	// Walk through the directory to find YAML task definitions
	err := ioFileIO.Walk(taskPath, func(path string) error {
		print(taskPath)
		if filepath.Ext(path) == ".yaml" {
			// Handle YAML files for task definitions
			if filepath.Base(path) == "config.yaml" {
				return nil
			}

			// Extract YAML definition and validate
			if yamlDef, err := ExtractYAML(ioFileIO, validator, path); err != nil {
				return err
			} else if yamlDef != nil {
				yamlDef.Partition = partition
				taskDefs[path] = yamlDef
			}
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("Error walking the task path %v: %v", taskPath, err), nil
	}

	if len(taskDefs) == 0 {
		return fmt.Errorf("Could not find YAML task definitions at path '%s' ", taskPath), nil
	}

	// Walk through template files to load and parse them
	for taskPath, taskDef := range taskDefs {
		tmpl := template.New("main")

		projectID, datasetID, tableName, errF := ioFileIO.ExtractInfo(taskPath)
		taskDef.Name = fmt.Sprintf("%s.%s.%s", projectID, datasetID, tableName)
		if errF != nil {
			return fmt.Errorf("Unexpected task path format %s (expected projectID.datasetID.Name) : %v", taskPath, errF), nil
		}
		config, funcs, errFunc := fn(taskDef, partition, projectID, datasetID, tableName)
		if errFunc != nil {
			return fmt.Errorf("task %s: %w", taskDef.Name, errFunc), nil
		}

		// Process the templatePath files (treat them as templates)
		err = ioFileIO.Walk(templatePath, func(path string) error {
			relPath, _ := filepath.Rel(templatePath, path)
			name := filepath.ToSlash(relPath)
			name = filepath.Base(path)

			// Read the template file
			bytes, err := ioFileIO.Read(path)
			if err != nil {
				return err
			}

			// Parse and register the template using its relative path as the name
			if _, err = tmpl.New(name).Funcs(funcs).Parse(string(bytes)); err != nil {
				return fmt.Errorf("error parsing template file %s: %v", path, err)
			}
			logger.Log.Info(fmt.Sprintf("Template successfully loaded %s", name))
			return nil
		})

		if err != nil {
			return err, nil
		}

		// Prepare the lazy renderer for the task definition
		renderer, err := RenderLazy(tmpl, config, funcs, taskDef, fieldName)
		if err != nil {
			return fmt.Errorf("Error preparing lazy template for file %s: %v", taskPath, err), nil
		}

		// Store the lazy renderer in the task definition for later execution
		taskDef.LazyRenderedField = renderer
	}

	return nil, taskDefs
}
