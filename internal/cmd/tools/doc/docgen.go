package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type (
	// command line config params
	config struct {
		rootDir    string
		verifyOnly bool
	}
)

// TODO: temporal directory missing 3 entries

var debug = false

var builtInTypes = map[string]bool{
	"int":        true,
	"int8":       true,
	"int16":      true,
	"int32":      true,
	"int64":      true,
	"uint":       true,
	"uint8":      true,
	"uint16":     true,
	"uint32":     true,
	"uint64":     true,
	"uintptr":    true,
	"float32":    true,
	"float64":    true,
	"complex64":  true,
	"complex128": true,
	"bool":       true,
	"byte":       true,
	"rune":       true,
	"string":     true,
	"error":      true,
}

func main() {
	var cfg config
	flag.StringVar(&cfg.rootDir, "rootDir", ".", "project root directory")
	flag.BoolVar(&cfg.verifyOnly, "verifyOnly", false,
		"don't automatically add headers, just verify all files")
	flag.Parse()

	// fakefolder
	// cfg.rootDir
	//err := filepath.Walk(cfg.rootDir, func(path string, info os.FileInfo, err error) error {
	err := filepath.Walk("temporal", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".go") {
			err := processFile(cfg, path)
			if err != nil {
				fmt.Printf("process file error: %v\n", err)
			}
		}
		return nil
	})

	if err != nil {
		fmt.Printf("Error walking the path %s: %v\n", cfg.rootDir, err)
	}
}

func processFile(cfg config, filePath string) error {
	typeAliasPattern := regexp.MustCompile(`^type\s+(\w+)\s+=\s+internal\.(\w+)`)
	varAliasPattern := regexp.MustCompile(`^var\s+(\w+)\s+=\s+internal\.(\w+)`)
	// This missed some group values
	//groupAliasPattern := regexp.MustCompile(`^\s*(\w+)\s*=\s*internal\.(\w+)$`)
	// This gets too many non-group values...
	groupAliasPattern := regexp.MustCompile(`^\s+(\w+)(?:\s+\w+)?\s*=\s*internal\.(\w+)$`)
	//funcAliasPattern := regexp.MustCompile(`(?m)^\s*func\s+(\w+)\s*\(.*\)\s*[^{]*\{\s*(?:return\s+)?internal\.(\w+)\(.*\)\s*\}\s*$`)
	funcAliasPattern := regexp.MustCompile(`(?m)^\s*func\s+(\w+)\s*\(.*\)\s*.*\{\s*(?:return\s+)?internal\.(\w+)\(.*\)\s*\}\s*$`)
	//docCommentPattern := regexp.MustCompile(`^// Exposed as:`)

	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("Failed to read file %s: %v\n", filePath, err)
	}

	fileContent := string(content)

	//// Match type aliases
	//typeMatches := typeAliasPattern.FindAllStringSubmatch(fileContent, -1)
	//for _, match := range typeMatches {
	//	fmt.Printf("File: %s | Type Alias: %s -> Internal: %s\n", filePath, match[1], match[2])
	//}
	//
	//// Match variable aliases
	//varMatches := varAliasPattern.FindAllStringSubmatch(fileContent, -1)
	//for _, match := range varMatches {
	//	fmt.Printf("File: %s | Variable Alias: %s -> Internal: %s\n", filePath, match[1], match[2])
	//}

	// Match grouped constants/aliases
	groupMatches := funcAliasPattern.FindAllStringSubmatch(fileContent, -1)
	for _, match := range groupMatches {
		if debug {
			fmt.Println("[FUNC]\npublic name: ", match[1], "\ninternal name: ", match[2], "\ngroup: ", "func")
		}
		err := addDocToInternalDefinition(cfg, match[2], "func", match[1])
		if err != nil {
			return fmt.Errorf("func: %v", err)
		}
	}

	// Must read file line-by-line to handle group aliases i.e. var () or const ()
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("Failed to open file %s: %v\n", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var group string
	for scanner.Scan() {
		line := scanner.Text()

		group = updateGrouping(group, line)

		// Handle type aliases
		if matches := typeAliasPattern.FindStringSubmatch(line); matches != nil {
			if debug {
				fmt.Println("[TYPE]\npublic name: ", matches[1], "\ninternal name: ", matches[2], "\ngroup: ", "type")
			}
			err := addDocToInternalDefinition(cfg, matches[2], "type", matches[1])
			if err != nil {
				return fmt.Errorf("type: %v", err)
			}
		}

		// Handle variable aliases
		if matches := varAliasPattern.FindStringSubmatch(line); matches != nil {
			if debug {
				fmt.Println("[VAR]\npublic name: ", matches[1], "\ninternal name: ", matches[2], "\ngroup: ", "var")
			}
			err := addDocToInternalDefinition(cfg, matches[2], "var", matches[1])
			if err != nil {
				return fmt.Errorf("variable: %v", err)
			}
		}

		// Handle group definitions
		if matches := groupAliasPattern.FindStringSubmatch(line); matches != nil {
			if debug {
				fmt.Println("[GROUP]\npublic name: ", matches[1], "\ninternal name: ", matches[2], "\ngroup: ", group)
			}
			//if matches[1] == "QueryTypeStackTrace" || matches[2] == "QueryTypeStackTrace" {
			//	fmt.Println("QueryTypeStackTrace")
			//}
			err := addDocToInternalDefinition(cfg, matches[2], group, matches[1])
			if err != nil {
				return fmt.Errorf("group: %v", err)
			}
		}

		// Handle function aliases
		//if matches := funcAliasPattern.FindStringSubmatch(line); matches != nil {
		//	//fmt.Println("[FUNC]\npublic name: ", matches[1], "\ninternal name: ", matches[2], "\ngroup: ", group)
		//	err := addDocToInternalDefinition(cfg, matches[2], "func", matches[1])
		//	if err != nil {
		//		return fmt.Errorf("func: %v", err)
		//	}
		//}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("Error reading file %s: %v\n", filePath, err)
	}
	return nil
}

func addDocToInternalDefinition(cfg config, internalName string, publicGroup string, publicName string) error {
	internalPath := filepath.Join(cfg.rootDir, "internal")
	var found bool

	// find the internal file to update
	err := filepath.Walk(internalPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("Error walking the path %s: %v\n", path, err)
		}

		// TODO: add && !found at the end, doing so before hand can mask other issues
		if strings.HasSuffix(path, ".go") {
			if foundInFile, err := addDocToFile(cfg, path, internalName, publicGroup, publicName); err != nil {
				return fmt.Errorf("addDocToFile error: %v", err)
			} else if foundInFile {
				found = true
			}
		}

		return nil
	})
	if !found {
		return fmt.Errorf("%s not found in any internal/ files", internalName)
	}
	if err != nil {
		return fmt.Errorf("Error walking the internal path %s: %v\n", internalPath, err)
	}
	return nil
}

func addDocToFile(cfg config, filePath string, internalName string, publicGroup string, publicName string) (bool, error) {
	tempFilePath := filePath + ".tmp"
	var output *os.File

	content, err := os.ReadFile(filePath)
	if err != nil {
		return false, fmt.Errorf("Failed to read file %s: %v\n", filePath, err)
	}
	if !strings.Contains(string(content), "package internal") {
		return false, nil
	}

	input, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("Failed to open file %s: %v\n", filePath, err)
		return false, err
	}
	defer input.Close()

	if !cfg.verifyOnly {
		output, err = os.Create(tempFilePath)
		if err != nil {
			fmt.Printf("Failed to create temp file %s: %v\n", tempFilePath, err)
			return false, err
		}
		defer output.Close()
	}

	scanner := bufio.NewScanner(input)
	found := false
	var group string
	for scanner.Scan() {
		line := scanner.Text()
		group = updateGrouping(group, line)
		lineFields := strings.Fields(line)

		//if internalName == "UnversionedBuildID" && strings.Contains(line, "const UnversionedBuildID = \"\"") {
		//	fmt.Println("[GetActivityInfo]")
		//	fmt.Println("lineFields", lineFields)
		//	fmt.Println("publicGroup", publicGroup)
		//	fmt.Println("group", group)
		//	fmt.Println("condition", group == "const" && len(lineFields) > 0 && lineFields[0] == internalName)
		//	fmt.Println(group == "const")
		//	fmt.Println(len(lineFields) > 0)
		//	fmt.Println(lineFields[0] == internalName)
		//}

		if !found {
			if group == publicGroup {
				if group == "const" && len(lineFields) > 0 && lineFields[0] == internalName {
					// non-group const matches
					found, err = handleDocumentation(cfg, publicName, internalName, filePath, tempFilePath, output, line)
					if err != nil {
						return false, err
					}
				} else if len(lineFields) > 1 && lineFields[0] == internalName && (strings.HasPrefix(lineFields[1], "struct") || lineFields[1] == "interface" || lineFields[1] == "=" || strings.HasPrefix(lineFields[1], "func(")) {
					// var() and type() group matches
					found, err = handleDocumentation(cfg, publicName, internalName, filePath, tempFilePath, output, line)
					if err != nil {
						return false, err
					}

				} else if group == "type" && len(lineFields) > 1 && builtInTypes[lineFields[1]] && strings.HasPrefix(line, "\t"+internalName) {
					// type() group with primitive type (non-struct/non-interface)
					found, err = handleDocumentation(cfg, publicName, internalName, filePath, tempFilePath, output, line)
					if err != nil {
						return false, err
					}
				} else if strings.HasPrefix(line, "func "+internalName) || strings.HasPrefix(line, "type "+internalName) || strings.HasPrefix(line, "var "+internalName) {
					// non-group matches for func, type, var
					found, err = handleDocumentation(cfg, publicName, internalName, filePath, tempFilePath, output, line)
					if err != nil {
						return false, err
					}
				}
			} else if group == "const" && publicGroup == "var" && len(lineFields) > 0 && lineFields[0] == internalName {
				// const that is aliased as a var
				found, err = handleDocumentation(cfg, publicName, internalName, filePath, tempFilePath, output, line)
				if err != nil {
					return false, err
				}
				// strings.HasPrefix(line, "func "+internalName) ||
			} else if strings.HasPrefix(line, "type "+internalName+" ") || strings.HasPrefix(line, "var "+internalName+" ") || strings.HasPrefix(line, "const "+internalName+" ") {
				// non-group matches for func, type, var, const
				found, err = handleDocumentation(cfg, publicName, internalName, filePath, tempFilePath, output, line)
				if err != nil {
					return false, err
				}
			}

		}

		if !cfg.verifyOnly {
			_, err = output.WriteString(line + "\n")
			if err != nil {
				fmt.Printf("Failed to write to temp file %s: %v\n", tempFilePath, err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading file %s: %v\n", filePath, err)
		return false, err
	}

	if !cfg.verifyOnly {
		err = os.Rename(tempFilePath, filePath)
		if err != nil {
			fmt.Printf("Failed to replace file %s with %s: %v\n", filePath, tempFilePath, err)
			return false, err
		}
	}

	return found, nil
}

// Helper function to handle adding or verifying documentation
func handleDocumentation(cfg config, publicName, internalName, filePath, tempFilePath string, output *os.File, line string) (bool, error) {
	docComment := fmt.Sprintf("\n//\n// Exposed as: %s", publicName)
	// TODO: This needs to be fixed, looking at the wrong line
	if !strings.Contains(line, docComment) {
		if !cfg.verifyOnly {
			_, err := output.WriteString(docComment + "\n")
			if err != nil {
				fmt.Printf("Failed to write to temp file %s: %v\n", tempFilePath, err)
				return false, err
			}
			fmt.Printf("Added documentation for %s/internal.%s in %s\n", publicName, internalName, filePath)
		} else {
			fmt.Printf("Documentation for %s/internal.%s in %s needs to be added\n", publicName, internalName, filePath)
		}
	}
	return true, nil
}

func updateGrouping(group string, line string) string {
	if strings.HasPrefix(line, ")") {
		group = ""
	} else if strings.HasPrefix(line, "type (") {
		group = "type"
	} else if strings.HasPrefix(line, "var (") {
		group = "var"
	} else if strings.HasPrefix(line, "const (") {
		group = "const"
	} else if strings.HasPrefix(line, "func ") {
		group = "func"
	}

	return group
}
