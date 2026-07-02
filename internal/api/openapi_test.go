package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestOpenAPISpecDocumentsAllServerOperations(t *testing.T) {
	t.Parallel()

	spec, err := loadOpenAPISpec()
	if err != nil {
		t.Fatalf("load OpenAPI spec: %v", err)
	}

	pathsValue, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI spec is missing paths")
	}

	var actual []documentedOperation
	for path, pathValue := range pathsValue {
		pathItem, ok := pathValue.(map[string]any)
		if !ok {
			t.Fatalf("path item %q is not an object", path)
		}
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			if _, ok := pathItem[strings.ToLower(method)]; ok {
				actual = append(actual, documentedOperation{Method: method, Path: path})
			}
		}
	}

	slices.SortFunc(actual, compareDocumentedOperations)
	expected := normalizedDocumentedOperations()
	if len(actual) != len(expected) {
		t.Fatalf("unexpected number of documented operations: got %d want %d\nactual=%s\nexpected=%s", len(actual), len(expected), formatOperations(actual), formatOperations(expected))
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("documented operations drifted\nactual=%s\nexpected=%s", formatOperations(actual), formatOperations(expected))
		}
	}
}

func TestOpenAPISpecIsValidJSON(t *testing.T) {
	t.Parallel()

	payload, err := readOpenAPISpecBytes()
	if err != nil {
		t.Fatalf("read OpenAPI spec: %v", err)
	}

	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
}

func compareDocumentedOperations(left, right documentedOperation) int {
	if left.Path == right.Path {
		return strings.Compare(left.Method, right.Method)
	}
	return strings.Compare(left.Path, right.Path)
}

func formatOperations(operations []documentedOperation) string {
	lines := make([]string, 0, len(operations))
	for _, operation := range operations {
		lines = append(lines, fmt.Sprintf("%s %s", operation.Method, operation.Path))
	}
	return strings.Join(lines, "\n")
}
