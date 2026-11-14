package templates

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/guregodevo/mario/fileio"
	"github.com/guregodevo/mario/logger"
	yaml "gopkg.in/yaml.v2"
)

type FuncMap func(taskDef *YamlTaskDefinition, partition, projectID, datasetID, tableName string) (map[string]interface{}, template.FuncMap, error)

// ArgsOnly is the FuncMap a caller gets without one: the task's args, its
// partition and its name are what a template can use, and no functions.
func ArgsOnly(taskDef *YamlTaskDefinition, partition, projectID, datasetID, tableName string) (map[string]interface{}, template.FuncMap, error) {
	config := map[string]interface{}{"partition": partition, "project_id": projectID, "dataset_id": datasetID, "table_name": tableName}
	for k, v := range taskDef.Args {
		config[k] = v
	}
	// `output` and `file` are known at run time only (a task type binds them
	// to the run's outputs and its directory); they are declared here so a
	// walk can parse the field.
	funcs := template.FuncMap{"output": func(name string) (string, error) {
		return "", fmt.Errorf("output %q is only known when the task runs", name)
	}, "file": func(path string) (string, error) {
		return "", fmt.Errorf("file %q is only read when the task runs", path)
	}, "exists": func(path string) (bool, error) {
		return false, fmt.Errorf("file %q is only looked for when the task runs", path)
	}}
	for name, fn := range StringFuncs() {
		funcs[name] = fn
	}
	return config, funcs, nil
}

// StringFuncs are the string functions every template has, at walk time and at run time: what a template
// needs to read an output as data. The value comes last, so they chain with a pipe:
// {{ output "test" | trim | hasSuffix "exit 0" }}, {{ file "event.json" | json "issue.number" }},
// {{ output "test" | tail 400 }}, {{ output "review" | match "(?im)^verdict: (pass|fail)" }}.
func StringFuncs() template.FuncMap {
	return template.FuncMap{
		"trim":      strings.TrimSpace,
		"contains":  func(part, s string) bool { return strings.Contains(s, part) },
		"hasPrefix": func(prefix, s string) bool { return strings.HasPrefix(s, prefix) },
		"hasSuffix": func(suffix, s string) bool { return strings.HasSuffix(s, suffix) },
		// default "none" .x is .x, or "none" when .x is empty or only space.
		"default": func(fallback, s string) string {
			if strings.TrimSpace(s) == "" {
				return fallback
			}
			return s
		},
		// head 1 .x is the first line of .x; tail 400 .x is its last 400 lines.
		"head": func(n int, s string) string { return lines(s, n, false) },
		"tail": func(n int, s string) string { return lines(s, n, true) },
		// json "workflow_run.id" .x is that field of the JSON text .x.
		"json": jsonField,
		// match "(?i)^verdict: (pass|fail)" .x is what the regular expression finds in .x: its first group
		// when it has one, else the whole match, and empty when it finds nothing.
		"match": match,
	}
}

// match is the first match of a regular expression (Go's syntax) in s: the first group when the expression
// has one, else all of it. Nothing found is empty, so `default` gives it a value and `if` reads it as false.
func match(pattern, s string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("match %q: not a regular expression: %v", pattern, err)
	}
	found := re.FindStringSubmatch(s)
	switch {
	case found == nil:
		return "", nil
	case len(found) > 1:
		return found[1], nil
	}
	return found[0], nil
}

// lines keeps the first n lines of s, or the last n. A final newline ends the last line, it is not a
// line of its own. n of zero or less keeps nothing.
func lines(s string, n int, fromEnd bool) string {
	if n <= 0 || s == "" {
		return ""
	}
	end := ""
	if strings.HasSuffix(s, "\n") {
		s, end = strings.TrimSuffix(s, "\n"), "\n"
	}
	all := strings.Split(s, "\n")
	if n >= len(all) {
		return s + end
	}
	if fromEnd {
		return strings.Join(all[len(all)-n:], "\n") + end
	}
	return strings.Join(all[:n], "\n") + end
}

// jsonField reads one field of a JSON text by a dotted path: "workflow_run.id", "issue.labels.0.name" (a
// number is a place in a list). A string comes out as it is, a number as written, an object or a list as
// JSON. A field that is not there, or null, is empty, so `default` gives it a value. Text that is not JSON
// is an error.
func jsonField(path, s string) (string, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("json %q: the text is not JSON: %v", path, err)
	}
	for _, key := range strings.Split(path, ".") {
		switch at := v.(type) {
		case map[string]interface{}:
			v = at[key]
		case []interface{}:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(at) {
				return "", nil
			}
			v = at[i]
		default:
			return "", nil
		}
	}
	switch at := v.(type) {
	case nil:
		return "", nil
	case string:
		return at, nil
	case json.Number:
		return at.String(), nil
	case bool:
		return strconv.FormatBool(at), nil
	}
	out, err := json.Marshal(v)
	return string(out), err
}

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
		if filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml" {
			// Handle YAML files for task definitions
			if filepath.Base(path) == "config.yaml" {
				return nil
			}

			// Extract YAML definition and validate
			if yamlDef, err := ExtractYAML(ioFileIO, validator, path); err != nil {
				// Name the file: a ten-step workflow refused for "colour is not
				// allowed" left the person to find which step (2026-10-04).
				rel, rerr := filepath.Rel(taskPath, path)
				if rerr != nil {
					rel = path
				}
				return fmt.Errorf("%s: %w", rel, err)
			} else if yamlDef != nil {
				yamlDef.Partition = partition
				// One file, several tasks with `each`: the key keeps the
				// real path, then "#" and the entry.
				if len(yamlDef.Each) == 0 {
					taskDefs[path] = yamlDef
				} else {
					for _, c := range yamlDef.Expand() {
						taskDefs[path+"#"+c.Item] = c
					}
				}
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

		infoPath := taskPath
		if taskDef.Item != "" {
			infoPath = strings.TrimSuffix(taskPath, "#"+taskDef.Item)
		}
		projectID, datasetID, tableName, errF := ioFileIO.ExtractInfo(infoPath)
		if errF != nil {
			return fmt.Errorf("Unexpected task path format %s (expected projectID.datasetID.Name) : %v", taskPath, errF), nil
		}
		if taskDef.Item != "" {
			tableName += "-" + taskDef.Item
		}
		taskDef.Name = fmt.Sprintf("%s.%s.%s", projectID, datasetID, tableName)
		for i, dep := range taskDef.Requires {
			taskDef.Requires[i] = dep.WithDefaults(projectID, datasetID)
		}
		if fn == nil {
			fn = ArgsOnly
		}
		config, funcs, errFunc := fn(taskDef, partition, projectID, datasetID, tableName)
		if errFunc != nil {
			return fmt.Errorf("task %s: %w", taskDef.Name, errFunc), nil
		}

		// Process the templatePath files (treat them as templates). No
		// template directory means the field renders from its own text.
		if templatePath == "" {
			renderer, err := RenderLazy(tmpl, config, funcs, taskDef, fieldName)
			if err != nil {
				return fmt.Errorf("Error preparing lazy template for file %s: %v", taskPath, err), nil
			}
			taskDef.LazyRenderedField = renderer
			continue
		}
		taskDef.Templated = true
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
