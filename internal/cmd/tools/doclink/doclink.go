// The MIT License
//
// Copyright (c) 2024 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type (
	// command line config params
	config struct {
		rootDir string
		fix     bool
	}
)

var missing = false

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
	if missing {
		log.Fatal("Missing documentation, see previous stdout for which objects. Re-run command with -fix to auto-generate missing docs.")
	}
}

func run() error {
	var cfg config
	flag.StringVar(&cfg.rootDir, "rootDir", ".", "project root directory")
	flag.BoolVar(&cfg.fix, "fix", false,
		"add links to internal types and functions that are exposed publicly")
	flag.Parse()
	publicToInternal := make(map[string]map[string]string)
	// Go through public packages and identify wrappers to internal types/funcs
	err := filepath.Walk(cfg.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("public walking %q: %v", path, err)
		}

		if info.IsDir() && (info.Name() == "internal" || info.Name() == "contrib") {
			return filepath.SkipDir
		}

		if strings.HasSuffix(path, "internalbindings.go") {
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to read file %s: %v", path, err)
			}
			defer func() {
				err = file.Close()
				if err != nil {
					log.Fatalf("failed to close file %s: %v", path, err)
				}
			}()

			res, err := processPublic(cfg, file)
			if err != nil {
				return fmt.Errorf("error while parsing public files: %v", err)
			}

			if len(res) > 0 {
				_, err = file.Seek(0, 0)
				if err != nil {
					log.Fatalf("Failed to rewind file: %v", err)
				}
				// TODO: remove
				packageName, err := extractPackageName(file)
				if err != nil {
					return fmt.Errorf("failed to extract package name: %v", err)
				}
				if packageMap, ok := publicToInternal[packageName]; !ok {
					publicToInternal[packageName] = res
				} else {
					for k, v := range res {
						if _, exists := packageMap[k]; exists {
							return fmt.Errorf("collision detected for package '%s': key '%s' exists in both maps (%s and %s)", packageName, k, packageMap[k], v)
						}
						packageMap[k] = v
					}
					publicToInternal[packageName] = packageMap
				}
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error walking the path %s: %v", cfg.rootDir, err)
	}

	// Go through internal files and match the definitions of private/public pairings
	err = filepath.Walk("internal", func(path string, info os.FileInfo, err error) error {
		if strings.HasSuffix(info.Name(), ".tmp") {
			return nil
		}
		if err != nil {
			return fmt.Errorf("walking %q: %v", path, err)
		}
		if info.IsDir() && info.Name() != "internal" {
			return filepath.SkipDir
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && !strings.HasPrefix(path, "internal/internal_") {
			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to read file %s: %v", path, err)
			}
			defer func() {
				err = file.Close()
				if err != nil {
					log.Fatalf("failed to close file %s: %v", path, err)
				}
			}()

			err = processInternal(cfg, file, publicToInternal)
			if err != nil {
				return fmt.Errorf("error while parsing internal files: %v", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error walking the path %s: %v", cfg.rootDir, err)
	}
	return nil
}

// Traverse the AST of public packages to identify wrappers for internal objects
func processPublic(cfg config, file *os.File) (map[string]string, error) {
	fs := token.NewFileSet()
	node, err := parser.ParseFile(fs, "", file, parser.AllErrors)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file : %v", err)
	}
	publicToInternal := make(map[string]string)
	ast.Inspect(node, func(n ast.Node) bool {
		if genDecl, ok := n.(*ast.GenDecl); ok {
			for _, spec := range genDecl.Specs {
				if typeSpec, typeOk := spec.(*ast.TypeSpec); typeOk {
					name := typeSpec.Name.Name
					if isPublic(name) {
						res := extractTypeValue(typeSpec.Type)
						if len(res) > 0 {
							publicToInternal[name] = res
						}
					}
				}
				if valueSpec, valueOk := spec.(*ast.ValueSpec); valueOk {
					if isTypeAssertion(valueSpec) {
						return true
					}
					name := valueSpec.Names
					if isPublic(name[0].Name) {
						res := checkValueSpec(valueSpec)
						if len(res) > 0 {
							publicToInternal[name[0].Name] = res
						}
					}
				}
			}
		}
		if funcDecl, ok := n.(*ast.FuncDecl); ok && isPublic(funcDecl.Name.Name) {
			isWrapper := checkFunction(funcDecl)
			if len(isWrapper) > 0 {
				publicToInternal[funcDecl.Name.Name] = isWrapper
			}
		}
		return true
	})
	return publicToInternal, nil
}

func isPublic(name string) bool {
	return strings.ToUpper(string(name[0])) == string(name[0])
}

func extractTypeValue(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StructType:
		for _, field := range t.Fields.List {
			res := extractTypeValue(field.Type)
			if len(res) > 0 {
				return res
			}
		}
	case *ast.InterfaceType:
		for _, method := range t.Methods.List {
			res := extractTypeValue(method.Type)
			if len(res) > 0 {
				return res
			}
		}
	case *ast.Ident:
		if strings.HasPrefix(t.Name, "internal.") {
			return strings.TrimPrefix(t.Name, "internal.")
		}
	case *ast.FuncType:
		for _, param := range t.Params.List {
			res := extractTypeValue(param.Type)
			if len(res) > 0 {
				return res
			}
		}
		if t.Results != nil {
			for _, result := range t.Results.List {
				res := extractTypeValue(result.Type)
				if len(res) > 0 {
					return res
				}
			}
		}
	case *ast.SelectorExpr:
		if ident, ok := t.X.(*ast.Ident); ok && ident.Name == "internal" {
			return t.Sel.Name
		}
	case *ast.BasicLit:
	// Do nothing
	default:
		//fmt.Printf("[WARN] Unsupported type: %T\n", t)
	}
	return ""
}

func checkValueSpec(spec *ast.ValueSpec) string {
	// Check if the type of the value spec contains "internal."
	if spec.Type != nil {
		res := extractTypeValue(spec.Type)
		if len(res) > 0 {
			return res
		}
	}

	// Check the expressions (values assigned) for "internal."
	for _, value := range spec.Values {
		res := extractTypeValue(value)
		if len(res) > 0 {
			return res
		}
	}

	return ""
}

// Check if a public function is a wrapper around an internal function
func checkFunction(funcDecl *ast.FuncDecl) string {
	// Ensure the function has a body
	if funcDecl.Body == nil {
		return ""
	}

	// Ensure the body has exactly one statement
	if len(funcDecl.Body.List) != 1 {
		return ""
	}

	// Check if the single statement is a return statement
	if retStmt, ok := funcDecl.Body.List[0].(*ast.ReturnStmt); ok {
		// Ensure the return statement directly calls an internal function
		for _, result := range retStmt.Results {
			if callExpr, ok := result.(*ast.CallExpr); ok {
				if res := isInternalFunctionCall(callExpr); len(res) > 0 {
					return res
				}
			}
		}
	}

	// Functions that don't return anything
	if exprStmt, ok := funcDecl.Body.List[0].(*ast.ExprStmt); ok {
		if callExpr, ok := exprStmt.X.(*ast.CallExpr); ok {
			if res := isInternalFunctionCall(callExpr); len(res) > 0 {
				return res
			}
		}
	}

	return ""
}

// Check if a call expression is calling an internal function
func isInternalFunctionCall(callExpr *ast.CallExpr) string {
	// Check if the function being called is a SelectorExpr (e.g., "internal.SomeFunction")
	if selExpr, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
		if pkgIdent, ok := selExpr.X.(*ast.Ident); ok && pkgIdent.Name == "internal" {
			return selExpr.Sel.Name
		}
	}
	return ""
}

// Check for type assertions like `var _ = internal.SomeType(nil)`
func isTypeAssertion(valueSpec *ast.ValueSpec) bool {
	for _, value := range valueSpec.Values {
		if callExpr, ok := value.(*ast.CallExpr); ok {
			if selExpr, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
				if pkgIdent, ok := selExpr.X.(*ast.Ident); ok && pkgIdent.Name == "internal" {
					return true
				}
			}
		}
	}
	return false
}

func extractPackageName(file *os.File) (string, error) {
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "package ") {
			// Split the line to extract the package name
			parts := strings.Fields(line)
			if len(parts) > 1 {
				return parts[1], nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scanner error: %e", err)
	}

	return "", fmt.Errorf("package declaration not found in %s", file.Name())
}

// Identify type/func definitions in the file and match to any private:public mappings.
// If mapping is identified, check if doc comment exists for such mapping.
func processInternal(cfg config, file *os.File, pairs map[string]map[string]string) error {
	scanner := bufio.NewScanner(file)
	newFile := ""
	exposedAs := "// Exposed as: "
	var inGroup, exposedLinks string
	var changesMade, inStruct bool
	for scanner.Scan() {
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)
		if isValidDefinition(trimmedLine, &inGroup, &inStruct) {
			for packageName, pair := range pairs {
				for public, private := range pair {
					if isValidDefinitionWithMatch(trimmedLine, private, inGroup, inStruct) {
						docLink := fmt.Sprintf("[go.temporal.io/sdk/%s.%s]", packageName, public)
						missingDoc := true
						if exposedLinks != "" {
							if strings.Contains(exposedLinks, docLink) {
								missingDoc = false
							}
						}
						if missingDoc {
							if cfg.fix {
								changesMade = true
								exposedLinks += docLink + ", "
								fmt.Printf("Fixed doc in %s for internal:%s to %s:%s\n", file.Name(), private, packageName, public)
							} else {
								missing = true
								fmt.Printf("Missing doc in %s for internal:%s to %s:%s\n", file.Name(), private, packageName, public)
							}
						}
					}
				}
			}
			if exposedLinks != "" {
				newFile += "//\n" + exposedAs + strings.TrimSuffix(exposedLinks, ", ") + "\n"
				exposedLinks = ""
			}
		} else if strings.HasPrefix(trimmedLine, exposedAs) {
			exposedLinks = strings.TrimPrefix(trimmedLine, exposedAs)
		}
		newFile += line + "\n"

	}

	if changesMade {
		absPath, err := filepath.Abs(file.Name())
		if err != nil {
			return fmt.Errorf("error getting absolute path: %v", err)
		}
		tempFilePath := absPath + ".tmp"

		formattedCode, err := format.Source([]byte(newFile))
		if err != nil {
			return fmt.Errorf("error formatting Go code: %v", err)

		}
		err = os.WriteFile(tempFilePath, formattedCode, 0644)
		if err != nil {
			return fmt.Errorf("error writing to file: %v", err)

		}
		err = os.Rename(tempFilePath, absPath)
		if err != nil {
			return fmt.Errorf("error renaming file: %v", err)
		}
	}

	return nil
}

func isValidDefinition(line string, inGroup *string, insideStruct *bool) bool {
	if strings.HasPrefix(line, "//") {
		return false
	}
	if strings.HasPrefix(line, "func ") {
		return true
	}

	if strings.HasSuffix(line, "struct {") {
		*insideStruct = true
		return true
	}

	if *insideStruct {
		if strings.HasSuffix(line, "}") && !strings.HasSuffix(line, "{}") {
			*insideStruct = false
		}
		return false
	}

	if *inGroup != "" {
		if line == ")" {
			*inGroup = ""
		}
		if line != "" {
			return true
		}
		return false
	}

	// Check if the line starts a grouped definition
	if strings.HasPrefix(line, "type (") ||
		strings.HasPrefix(line, "const (") ||
		strings.HasPrefix(line, "var (") {
		*inGroup = strings.Fields(line)[0]
		return false
	}

	// Handle single-line struct, variable, or function definitions
	if strings.HasPrefix(line, "var ") ||
		strings.HasPrefix(line, "const ") ||
		strings.HasPrefix(line, "type ") {
		return true
	}
	return false
}

func isValidDefinitionWithMatch(line, private string, inGroup string, insideStruct bool) bool {
	tokens := strings.Fields(line)
	if strings.HasPrefix(line, "func "+private+"(") {
		return true
	}

	if strings.HasSuffix(line, " struct {") {
		for _, strToken := range tokens {
			if strToken == private {
				return true
			}
		}
		return false
	}

	if insideStruct {
		fmt.Println("should never hit")
		return false
	}

	if inGroup == "const" || inGroup == "var" {
		return tokens[0] == private
	} else if inGroup == "type" {
		return len(tokens) > 2 && tokens[2] == private
	}

	// Handle single-line struct, variable, or function definitions
	if strings.HasPrefix(line, "var ") ||
		strings.HasPrefix(line, "const ") ||
		strings.HasPrefix(line, "type ") {
		for _, strToken := range tokens {
			if strToken == private {
				return true
			}
		}
	}
	return false
}
